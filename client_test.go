package myvocalv3dialogue

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type testServer struct {
	server   *httptest.Server
	url      string
	received chan map[string]any
	handler  func(*websocket.Conn, map[string]any)
}

func newTestServer(
	t *testing.T,
	handler func(*websocket.Conn, map[string]any),
) *testServer {
	t.Helper()
	value := &testServer{
		received: make(chan map[string]any, 32),
		handler:  handler,
	}
	upgrader := websocket.Upgrader{}
	value.server = httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			if request.Header.Get("api-key") != "test-key" {
				t.Errorf("missing api-key header")
			}
			conn, err := upgrader.Upgrade(writer, request, nil)
			if err != nil {
				t.Errorf("upgrade: %v", err)
				return
			}
			defer conn.Close()
			for {
				var message map[string]any
				if err := conn.ReadJSON(&message); err != nil {
					return
				}
				value.received <- message
				handler(conn, message)
			}
		}))
	value.url = "ws" + strings.TrimPrefix(value.server.URL, "http") +
		"/enterprise/v1/text-to-dialogue/websocket/multi"
	return value
}

func (server *testServer) Close() {
	server.server.Close()
}

func TestOperationsFinalsAndClose1000(t *testing.T) {
	server := newTestServer(t, func(conn *websocket.Conn, message map[string]any) {
		if boolean(message["close_socket"]) {
			_ = conn.WriteJSON(map[string]any{"context_id": "a", "is_final": true})
			_ = conn.WriteJSON(map[string]any{"context_id": "b", "is_final": true})
			_ = conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(1000, "done"),
				time.Now().Add(time.Second),
			)
		}
	})
	defer server.Close()
	client := testClient(server.url, Callbacks{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	must(t, client.OpenContext(ContextOptions{ContextID: "a", Voices: []string{"voice-a"}}))
	must(t, client.OpenContext(ContextOptions{ContextID: "b", Voices: []string{"voice-b"}}))
	must(t, client.SendInputs("a", []Input{{Text: "Hello", VoiceID: "voice-a", NewTurn: true}}))
	must(t, client.Flush("a"))
	must(t, client.KeepAlive("a"))
	result, err := client.CloseSocket(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.CloseCode != 1000 || !client.ContextDone("a") || !client.ContextDone("b") {
		t.Fatalf("unexpected close result: %+v", result)
	}
}

func TestAudioOrderTurnFinalAndContextFinal(t *testing.T) {
	server := newTestServer(t, func(conn *websocket.Conn, message map[string]any) {
		if _, ok := message["voices"]; ok {
			value, _ := json.Marshal(map[string]any{
				"context_id": "ctx",
				"audio":      base64.StdEncoding.EncodeToString([]byte("one")),
			})
			_ = conn.WriteMessage(websocket.BinaryMessage, value)
			_ = conn.WriteJSON(map[string]any{
				"context_id":              "ctx",
				"audio":                   base64.StdEncoding.EncodeToString([]byte("two")),
				"is_final_audio_for_turn": true,
			})
		}
		if boolean(message["close_context"]) {
			_ = conn.WriteJSON(map[string]any{"context_id": "ctx", "is_final": true})
		}
		if boolean(message["close_socket"]) {
			_ = conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(1000, "done"),
				time.Now().Add(time.Second),
			)
		}
	})
	defer server.Close()
	var lock sync.Mutex
	var audio []byte
	client := testClient(server.url, Callbacks{
		OnAudio: func(_ string, value []byte, _ map[string]any) {
			lock.Lock()
			audio = append(audio, value...)
			lock.Unlock()
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	must(t, client.Connect(ctx))
	must(t, client.OpenContext(ContextOptions{ContextID: "ctx", Voices: []string{"voice-a"}}))
	time.Sleep(50 * time.Millisecond)
	if client.ContextDone("ctx") {
		t.Fatal("turn final must not complete context")
	}
	result, err := client.CloseContext(ctx, "ctx")
	if err != nil || result.ContextID != "ctx" {
		t.Fatalf("close context: %+v %v", result, err)
	}
	lock.Lock()
	got := string(audio)
	lock.Unlock()
	if got != "onetwo" {
		t.Fatalf("audio order: %q", got)
	}
	_, err = client.CloseSocket(ctx)
	if err != nil {
		t.Fatal(err)
	}
}

func TestProtocolErrorStickyAcrossClose1000(t *testing.T) {
	server := newTestServer(t, func(conn *websocket.Conn, message map[string]any) {
		if _, ok := message["voices"]; ok {
			_ = conn.WriteJSON(map[string]any{
				"code":    "INVALID_PARAMETER",
				"field":   "inputs.voice_id",
				"message": "invalid voice",
			})
			_ = conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(1000, "done"),
				time.Now().Add(time.Second),
			)
		}
	})
	defer server.Close()
	client := testClient(server.url, Callbacks{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	must(t, client.Connect(ctx))
	must(t, client.OpenContext(ContextOptions{ContextID: "ctx", Voices: []string{"voice-a"}}))
	_, err := client.WaitClosed(ctx)
	protocol, ok := err.(*ProtocolError)
	if !ok || protocol.Code != "INVALID_PARAMETER" ||
		protocol.Field != "inputs.voice_id" {
		t.Fatalf("expected sticky protocol error, got %#v", err)
	}
}

func TestCallbackPanicIsolatedAndLateSendRejected(t *testing.T) {
	server := newTestServer(t, func(conn *websocket.Conn, message map[string]any) {
		if boolean(message["close_context"]) {
			_ = conn.WriteJSON(map[string]any{"context_id": "ctx", "is_final": true})
		}
		if boolean(message["close_socket"]) {
			_ = conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(1000, "done"),
				time.Now().Add(time.Second),
			)
		}
	})
	defer server.Close()
	var errorsMu sync.Mutex
	var errors []error
	client := testClient(server.url, Callbacks{
		OnEvent: func(ServerEvent) { panic("boom") },
		OnError: func(err error) {
			errorsMu.Lock()
			errors = append(errors, err)
			errorsMu.Unlock()
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	must(t, client.Connect(ctx))
	must(t, client.OpenContext(ContextOptions{ContextID: "ctx", Voices: []string{"voice-a"}}))
	_, err := client.CloseContext(ctx, "ctx")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		errorsMu.Lock()
		defer errorsMu.Unlock()
		return len(errors) > 0
	})
	errorsMu.Lock()
	count := len(errors)
	errorsMu.Unlock()
	if count == 0 {
		t.Fatal("callback panic was not reported")
	}
	err = client.SendInputs("ctx", []Input{{Text: "late", VoiceID: "voice-a"}})
	if _, ok := err.(*StateError); !ok {
		t.Fatalf("expected StateError, got %#v", err)
	}
	_, err = client.CloseSocket(ctx)
	if err != nil {
		t.Fatal(err)
	}
}

func TestDuplicateCloseOperationsAreIdempotent(t *testing.T) {
	var lock sync.Mutex
	contextCloses := 0
	socketCloses := 0
	server := newTestServer(t, func(conn *websocket.Conn, message map[string]any) {
		if boolean(message["close_context"]) {
			lock.Lock()
			contextCloses++
			lock.Unlock()
			_ = conn.WriteJSON(map[string]any{"context_id": "ctx", "is_final": true})
		}
		if boolean(message["close_socket"]) {
			lock.Lock()
			socketCloses++
			lock.Unlock()
			_ = conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(1000, "done"),
				time.Now().Add(time.Second),
			)
		}
	})
	defer server.Close()
	finals := 0
	closes := 0
	client := testClient(server.url, Callbacks{
		OnContextFinal: func(ContextResult) {
			lock.Lock()
			finals++
			lock.Unlock()
		},
		OnClose: func(int, string) {
			lock.Lock()
			closes++
			lock.Unlock()
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	must(t, client.Connect(ctx))
	must(t, client.OpenContext(ContextOptions{ContextID: "ctx", Voices: []string{"voice-a"}}))
	first, err := client.CloseContext(ctx, "ctx")
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.CloseContext(ctx, "ctx")
	if err != nil || first.ContextID != second.ContextID {
		t.Fatalf("duplicate context close: %+v %v", second, err)
	}
	firstClose, err := client.CloseSocket(ctx)
	if err != nil {
		t.Fatal(err)
	}
	secondClose, err := client.CloseSocket(ctx)
	if err != nil || firstClose.CloseCode != secondClose.CloseCode {
		t.Fatalf("duplicate socket close: %+v %v", secondClose, err)
	}
	waitFor(t, func() bool {
		lock.Lock()
		defer lock.Unlock()
		return finals == 1 && closes == 1
	})
	lock.Lock()
	defer lock.Unlock()
	if contextCloses != 1 || socketCloses != 1 || finals != 1 || closes != 1 {
		t.Fatalf("unexpected close counts")
	}
}

func TestPolicyCloseWithoutJSONIsProtocolError(t *testing.T) {
	server := newTestServer(t, func(conn *websocket.Conn, message map[string]any) {
		if _, ok := message["voices"]; ok {
			_ = conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(1008, "policy"),
				time.Now().Add(time.Second),
			)
		}
	})
	defer server.Close()
	client := testClient(server.url, Callbacks{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	must(t, client.Connect(ctx))
	must(t, client.OpenContext(ContextOptions{ContextID: "ctx", Voices: []string{"voice-a"}}))
	_, err := client.WaitClosed(ctx)
	protocol, ok := err.(*ProtocolError)
	if !ok || protocol.Code != "POLICY_CLOSE" || protocol.CloseCode != 1008 {
		t.Fatalf("expected policy protocol error, got %#v", err)
	}
}

func TestSocketCloseTimeoutUsesTimeoutCategory(t *testing.T) {
	server := newTestServer(t, func(*websocket.Conn, map[string]any) {})
	defer server.Close()
	client := testClient(server.url, Callbacks{})
	connectCtx, connectCancel := context.WithTimeout(
		context.Background(), time.Second)
	defer connectCancel()
	must(t, client.Connect(connectCtx))
	closeCtx, closeCancel := context.WithTimeout(
		context.Background(), 20*time.Millisecond)
	defer closeCancel()
	_, err := client.CloseSocket(closeCtx)
	if _, ok := err.(*TimeoutError); !ok {
		t.Fatalf("expected TimeoutError, got %#v", err)
	}
}

func TestActiveContextLimitAndContextCloseTimeout(t *testing.T) {
	server := newTestServer(t, func(*websocket.Conn, map[string]any) {})
	defer server.Close()
	client := testClient(server.url, Callbacks{})
	connectCtx, connectCancel := context.WithTimeout(
		context.Background(), time.Second)
	defer connectCancel()
	must(t, client.Connect(connectCtx))
	for index := 0; index < 5; index++ {
		must(t, client.OpenContext(ContextOptions{
			ContextID: "ctx-" + string(rune('0'+index)),
			Voices:    []string{"voice-a"},
		}))
	}
	err := client.OpenContext(ContextOptions{
		ContextID: "ctx-5",
		Voices:    []string{"voice-a"},
	})
	if _, ok := err.(*StateError); !ok {
		t.Fatalf("expected context limit StateError, got %#v", err)
	}
	closeCtx, closeCancel := context.WithTimeout(
		context.Background(), 20*time.Millisecond)
	defer closeCancel()
	_, err = client.CloseContext(closeCtx, "ctx-0")
	if _, ok := err.(*TimeoutError); !ok {
		t.Fatalf("expected context TimeoutError, got %#v", err)
	}
	client.Abort()
}

func TestClose1000BeforeContextFinalIsProtocolError(t *testing.T) {
	server := newTestServer(t, func(conn *websocket.Conn, message map[string]any) {
		if _, ok := message["voices"]; ok {
			_ = conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(1000, "early"),
				time.Now().Add(time.Second),
			)
		}
	})
	defer server.Close()
	client := testClient(server.url, Callbacks{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	must(t, client.Connect(ctx))
	must(t, client.OpenContext(ContextOptions{
		ContextID: "ctx", Voices: []string{"voice-a"},
	}))
	_, err := client.WaitClosed(ctx)
	protocol, ok := err.(*ProtocolError)
	if !ok || protocol.Code != "MISSING_FINAL" {
		t.Fatalf("expected MISSING_FINAL, got %#v", err)
	}
}

func TestContextStringErrorIsolatedFromSibling(t *testing.T) {
	server := newTestServer(t, func(conn *websocket.Conn, message map[string]any) {
		if message["context_id"] == "bad" && message["voices"] != nil {
			_ = conn.WriteJSON(map[string]any{
				"error":      "TASK_FAILED",
				"message":    "bad context",
				"context_id": "bad",
			})
		}
	})
	defer server.Close()
	client := testClient(server.url, Callbacks{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	must(t, client.Connect(ctx))
	must(t, client.OpenContext(ContextOptions{
		ContextID: "bad", Voices: []string{"voice-a"},
	}))
	must(t, client.OpenContext(ContextOptions{
		ContextID: "good", Voices: []string{"voice-b"},
	}))
	time.Sleep(50 * time.Millisecond)
	must(t, client.SendInputs("good", []Input{{
		Text: "continue", VoiceID: "voice-b",
	}}))
	_, err := client.CloseContext(ctx, "bad")
	protocol, ok := err.(*ProtocolError)
	if !ok || protocol.Code != "TASK_FAILED" || protocol.ContextID != "bad" {
		t.Fatalf("expected context protocol error, got %#v", err)
	}
	client.Abort()
}

func TestSlowControlCallbackDoesNotBlockReader(t *testing.T) {
	server := newTestServer(t, func(conn *websocket.Conn, message map[string]any) {
		if message["voices"] != nil {
			_ = conn.WriteJSON(map[string]any{
				"context_id": "ctx", "status": "progress",
			})
			_ = conn.WriteJSON(map[string]any{
				"context_id": "ctx", "is_final": true,
			})
		}
	})
	defer server.Close()
	started := make(chan struct{})
	release := make(chan struct{})
	client := testClient(server.url, Callbacks{
		OnEvent: func(ServerEvent) {
			select {
			case <-started:
			default:
				close(started)
			}
			<-release
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	must(t, client.Connect(ctx))
	must(t, client.OpenContext(ContextOptions{
		ContextID: "ctx", Voices: []string{"voice-a"},
	}))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("callback did not start")
	}
	time.Sleep(50 * time.Millisecond)
	if !client.ContextDone("ctx") {
		t.Fatal("slow control callback blocked reader")
	}
	close(release)
	client.Abort()
}

func TestControlEventStormIsBounded(t *testing.T) {
	server := newTestServer(t, func(conn *websocket.Conn, message map[string]any) {
		if message["voices"] != nil {
			for index := 0; index < 200; index++ {
				_ = conn.WriteJSON(map[string]any{
					"context_id": "ctx", "sequence": index,
				})
			}
			_ = conn.WriteJSON(map[string]any{
				"context_id": "ctx", "is_final": true,
			})
		}
	})
	defer server.Close()
	started := make(chan struct{})
	release := make(chan struct{})
	client := testClient(server.url, Callbacks{
		OnEvent: func(ServerEvent) {
			select {
			case <-started:
			default:
				close(started)
			}
			<-release
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	must(t, client.Connect(ctx))
	must(t, client.OpenContext(ContextOptions{
		ContextID: "ctx", Voices: []string{"voice-a"},
	}))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("callback did not start")
	}
	waitFor(t, func() bool { return client.ContextDone("ctx") })
	if !client.ContextDone("ctx") {
		t.Fatal("control event storm blocked reader")
	}
	if len(client.controlQueue) > 16 {
		t.Fatalf("control queue exceeded bound: %d", len(client.controlQueue))
	}
	close(release)
	client.Abort()
}

func TestCloseSocketAfterTerminalClosePreservesStickyError(t *testing.T) {
	server := newTestServer(t, func(conn *websocket.Conn, message map[string]any) {})
	defer server.Close()
	client := testClient(server.url, Callbacks{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	must(t, client.Connect(ctx))
	_ = client.conn.WriteJSON(map[string]any{})
	client.setSticky(&ProtocolError{
		Code: "SESSION_FAILED", Message: "failed",
	})
	client.mu.Lock()
	client.connected = false
	client.mu.Unlock()
	_, err := client.CloseSocket(ctx)
	protocol, ok := err.(*ProtocolError)
	if !ok || protocol.Code != "SESSION_FAILED" {
		t.Fatalf("expected original protocol error, got %#v", err)
	}
	client.Abort()
}

func testClient(endpoint string, callbacks Callbacks) *Client {
	options := DefaultConnectionOptions()
	options.Endpoint = endpoint
	return NewClient("test-key", options, callbacks)
}

func boolean(value any) bool {
	result, _ := value.(bool)
	return result
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !condition() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
}

func decodeJSON(t *testing.T, value []byte) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(value, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
