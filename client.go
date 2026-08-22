package myvocalv3dialogue

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

type Callbacks struct {
	OnAudio        func(contextID string, audio []byte, alignment map[string]any)
	OnEvent        func(event ServerEvent)
	OnContextFinal func(result ContextResult)
	OnClose        func(code int, reason string)
	OnError        func(err error)
}

type contextState struct {
	options ContextOptions
	done    chan struct{}
	once    sync.Once
	closing bool
	final   bool
	err     error
	audio   bytes.Buffer
	result  ContextResult
}

type Client struct {
	apiKey          string
	options         ConnectionOptions
	callbacks       Callbacks
	accumulateAudio bool

	mu            sync.Mutex
	writeMu       sync.Mutex
	conn          *websocket.Conn
	connected     bool
	closingSocket bool
	aborting      bool
	contexts      map[string]*contextState
	sticky        error
	closeCode     int
	closeResult   *CloseResult
	closeDone     chan struct{}
	closeOnce     sync.Once
	controlQueue  chan callbackJob
	callbackOnce  sync.Once
}

type callbackJob struct {
	run      func()
	critical bool
}

func NewClient(
	apiKey string,
	options ConnectionOptions,
	callbacks Callbacks,
) *Client {
	return NewClientWithAudioAccumulation(
		apiKey, options, callbacks, false)
}

func NewClientWithAudioAccumulation(
	apiKey string,
	options ConnectionOptions,
	callbacks Callbacks,
	accumulateAudio bool,
) *Client {
	return &Client{
		apiKey:          apiKey,
		options:         options,
		callbacks:       callbacks,
		accumulateAudio: accumulateAudio,
		contexts:        make(map[string]*contextState),
		closeCode:       -1,
		closeDone:       make(chan struct{}),
		controlQueue:    make(chan callbackJob, 16),
	}
}

func (client *Client) Connect(ctx context.Context) error {
	if client.apiKey == "" {
		return &ValidationError{"api_key must not be empty"}
	}
	rawURL, err := client.options.BuildURL()
	if err != nil {
		return err
	}
	client.mu.Lock()
	if client.conn != nil {
		client.mu.Unlock()
		return &StateError{"client is already connected"}
	}
	client.mu.Unlock()

	header := http.Header{}
	header.Set("api-key", client.apiKey)
	conn, response, err := websocket.DefaultDialer.DialContext(
		ctx, rawURL, header)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil &&
			(response.StatusCode == http.StatusUnauthorized ||
				response.StatusCode == http.StatusForbidden) {
			return &AuthenticationError{
				Message: "WebSocket authentication failed",
			}
		}
		return &TransportError{
			Message: "WebSocket connection failed",
			Cause:   err,
		}
	}
	client.mu.Lock()
	client.conn = conn
	client.connected = true
	client.mu.Unlock()
	client.callbackOnce.Do(func() { go client.callbackLoop() })
	go client.readLoop()
	return nil
}

func (client *Client) OpenContext(options ContextOptions) error {
	value, err := options.Wire()
	if err != nil {
		return err
	}
	if err := client.requireConnected(); err != nil {
		return err
	}
	client.mu.Lock()
	if client.closingSocket {
		client.mu.Unlock()
		return &StateError{"socket is closing"}
	}
	if _, exists := client.contexts[options.ContextID]; exists {
		client.mu.Unlock()
		return &StateError{"context_id is already registered"}
	}
	active := 0
	for _, state := range client.contexts {
		if !state.final {
			active++
		}
	}
	if active >= 5 {
		client.mu.Unlock()
		return &StateError{"at most 5 active contexts are allowed"}
	}
	client.contexts[options.ContextID] = &contextState{
		options: options,
		done:    make(chan struct{}),
	}
	client.mu.Unlock()
	return client.send(value)
}

func (client *Client) SendInputs(
	contextID string, inputs []Input,
) error {
	state, err := client.requireOpenContext(contextID)
	if err != nil {
		return err
	}
	if len(inputs) == 0 {
		return &ValidationError{"inputs must not be empty"}
	}
	registered := make(map[string]struct{}, len(state.options.Voices))
	for _, voice := range state.options.Voices {
		registered[voice] = struct{}{}
	}
	values := make([]map[string]any, 0, len(inputs))
	for _, input := range inputs {
		if err := input.validate(); err != nil {
			return err
		}
		if _, exists := registered[input.VoiceID]; !exists {
			return &ValidationError{
				"input voice_id must be registered",
			}
		}
		values = append(values, input.wire())
	}
	return client.send(map[string]any{
		"context_id": contextID,
		"inputs":     values,
	})
}

func (client *Client) Flush(contextID string) error {
	if _, err := client.requireOpenContext(contextID); err != nil {
		return err
	}
	return client.send(map[string]any{
		"context_id": contextID,
		"flush":      true,
	})
}

func (client *Client) KeepAlive(contextID string) error {
	if _, err := client.requireOpenContext(contextID); err != nil {
		return err
	}
	return client.send(map[string]any{
		"context_id": contextID,
		"keep_alive": true,
	})
}

func (client *Client) CloseContext(
	ctx context.Context, contextID string,
) (ContextResult, error) {
	if err := client.requireConnected(); err != nil {
		return ContextResult{}, err
	}
	client.mu.Lock()
	state := client.contexts[contextID]
	if state == nil {
		client.mu.Unlock()
		return ContextResult{}, &StateError{"unknown context_id"}
	}
	if state.final {
		result := state.result
		stateErr := state.err
		result.Audio = append([]byte(nil), result.Audio...)
		client.mu.Unlock()
		return result, stateErr
	}
	sendClose := !state.closing
	state.closing = true
	client.mu.Unlock()
	if sendClose {
		if err := client.send(map[string]any{
			"context_id":    contextID,
			"close_context": true,
		}); err != nil {
			return ContextResult{}, err
		}
	}
	select {
	case <-ctx.Done():
		return ContextResult{}, &TimeoutError{
			Message: "timed out waiting for context final",
			Cause:   ctx.Err(),
		}
	case <-state.done:
	}
	if state.err != nil {
		return ContextResult{}, state.err
	}
	if err := client.stickyError(); err != nil {
		return ContextResult{}, err
	}
	client.mu.Lock()
	result := state.result
	result.Audio = append([]byte(nil), result.Audio...)
	client.mu.Unlock()
	return result, nil
}

func (client *Client) CloseSocket(
	ctx context.Context,
) (CloseResult, error) {
	if err := client.stickyError(); err != nil {
		return CloseResult{}, err
	}
	client.mu.Lock()
	if client.closeResult != nil {
		result := *client.closeResult
		client.mu.Unlock()
		return result, client.stickyError()
	}
	if client.conn == nil {
		client.mu.Unlock()
		return CloseResult{}, &StateError{"client has not connected"}
	}
	sendClose := !client.closingSocket
	if sendClose && !client.connected {
		client.mu.Unlock()
		return CloseResult{}, &StateError{"client is not connected"}
	}
	client.closingSocket = true
	active := make([]*contextState, 0, len(client.contexts))
	for _, state := range client.contexts {
		if !state.final {
			active = append(active, state)
		}
	}
	client.mu.Unlock()
	if sendClose {
		if err := client.send(map[string]any{"close_socket": true}); err != nil {
			return CloseResult{}, err
		}
		for _, state := range active {
			select {
			case <-ctx.Done():
				client.Abort()
				return CloseResult{}, &TimeoutError{
					Message: "timed out waiting for context final",
					Cause:   ctx.Err(),
				}
			case <-state.done:
			}
			if err := client.stickyError(); err != nil {
				return CloseResult{}, err
			}
		}
	}
	result, err := client.WaitClosed(ctx)
	if _, ok := err.(*TimeoutError); ok {
		client.Abort()
	}
	return result, err
}

func (client *Client) WaitClosed(
	ctx context.Context,
) (CloseResult, error) {
	client.mu.Lock()
	if client.conn == nil {
		client.mu.Unlock()
		return CloseResult{}, &StateError{"client has not connected"}
	}
	client.mu.Unlock()
	select {
	case <-ctx.Done():
		return CloseResult{}, &TimeoutError{
			Message: "timed out waiting for socket close",
			Cause:   ctx.Err(),
		}
	case <-client.closeDone:
	}
	if err := client.stickyError(); err != nil {
		return CloseResult{}, err
	}
	client.mu.Lock()
	code := client.closeCode
	aborting := client.aborting
	client.mu.Unlock()
	if !aborting && code != 1000 {
		return CloseResult{}, &TransportError{
			Message: "WebSocket closed with non-normal code",
		}
	}
	result := CloseResult{CloseCode: code}
	client.mu.Lock()
	client.closeResult = &result
	client.mu.Unlock()
	return result, nil
}

func (client *Client) Abort() {
	client.mu.Lock()
	client.aborting = true
	conn := client.conn
	client.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

func (client *Client) ContextDone(contextID string) bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	state := client.contexts[contextID]
	return state != nil && state.final
}

func (client *Client) send(value map[string]any) error {
	if err := client.stickyError(); err != nil {
		return err
	}
	client.mu.Lock()
	conn := client.conn
	connected := client.connected
	client.mu.Unlock()
	if conn == nil || !connected {
		return &StateError{"client is not connected"}
	}
	client.writeMu.Lock()
	err := conn.WriteJSON(value)
	client.writeMu.Unlock()
	if err != nil {
		transport := &TransportError{
			Message: "WebSocket send failed",
			Cause:   err,
		}
		client.setSticky(transport)
		return transport
	}
	return nil
}

func (client *Client) readLoop() {
	for {
		messageType, payload, err := client.conn.ReadMessage()
		if err != nil {
			client.finishRead(err)
			return
		}
		if messageType != websocket.TextMessage &&
			messageType != websocket.BinaryMessage {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal(payload, &event); err != nil {
			protocol := &ProtocolError{
				Code:    "INVALID_JSON",
				Message: "server frame was not JSON",
			}
			client.setSticky(protocol)
			client.invokeError(protocol)
			continue
		}
		client.handleEvent(event)
	}
}

func (client *Client) handleEvent(event map[string]any) {
	if protocol := protocolErrorFrom(event); protocol != nil {
		client.mu.Lock()
		state := client.contexts[protocol.ContextID]
		if protocol.ContextID != "" && state != nil {
			if !state.final {
				state.err = protocol
				state.final = true
				state.result = ContextResult{
					ContextID: protocol.ContextID,
					Audio: append(
						[]byte(nil), state.audio.Bytes()...),
				}
				state.once.Do(func() { close(state.done) })
			}
		} else {
			state = nil
		}
		client.mu.Unlock()
		if state == nil {
			client.setSticky(protocol)
		}
		client.invokeError(protocol)
		return
	}
	contextID, _ := event["context_id"].(string)
	client.mu.Lock()
	state := client.contexts[contextID]
	client.mu.Unlock()

	if encoded, ok := event["audio"].(string); ok && encoded != "" {
		audio, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			protocol := &ProtocolError{
				Code:    "INVALID_AUDIO",
				Message: "audio was not valid base64",
			}
			client.setSticky(protocol)
			client.invokeError(protocol)
			return
		}
		if state == nil {
			protocol := &ProtocolError{
				Code:    "UNKNOWN_CONTEXT",
				Field:   "context_id",
				Message: "audio event referenced an unknown context",
			}
			client.setSticky(protocol)
			client.invokeError(protocol)
			return
		}
		if client.accumulateAudio {
			client.mu.Lock()
			_, _ = state.audio.Write(audio)
			client.mu.Unlock()
		}
		alignment, _ := event["alignment"].(map[string]any)
		client.invokeAudio(contextID, audio, alignment)
	}

	final, _ := event["is_final"].(bool)
	if final && state == nil {
		protocol := &ProtocolError{
			Code:    "UNKNOWN_CONTEXT",
			Field:   "context_id",
			Message: "final event referenced an unknown context",
		}
		client.setSticky(protocol)
		client.invokeError(protocol)
		return
	}
	if final {
		client.mu.Lock()
		if !state.final {
			state.final = true
			state.result = ContextResult{
				ContextID: contextID,
				Audio:     append([]byte(nil), state.audio.Bytes()...),
			}
		}
		client.mu.Unlock()
	}

	client.invokeEvent(ServerEvent{Raw: cloneMap(event)})
	if final {
		client.invokeContextFinal(state.result)
		state.once.Do(func() { close(state.done) })
	}
}

func (client *Client) finishRead(readErr error) {
	var closeErr *websocket.CloseError
	code := -1
	if errors.As(readErr, &closeErr) {
		code = closeErr.Code
	}
	client.mu.Lock()
	client.closeCode = code
	client.connected = false
	aborting := client.aborting
	unfinished := false
	for _, state := range client.contexts {
		if !state.final {
			unfinished = true
			break
		}
	}
	client.mu.Unlock()
	if !aborting {
		if code == 1008 {
			client.setSticky(&ProtocolError{
				Code:      "POLICY_CLOSE",
				Message:   "WebSocket policy close",
				CloseCode: 1008,
			})
		} else if code != 1000 {
			client.setSticky(&TransportError{
				Message: "WebSocket closed with non-normal code",
				Cause:   readErr,
			})
		} else if unfinished && client.stickyError() == nil {
			client.setSticky(&ProtocolError{
				Code:    "MISSING_FINAL",
				Message: "socket closed before every context was final",
			})
		}
	}
	reason := ""
	if closeErr != nil {
		reason = closeErr.Text
	}
	client.invokeClose(code, reason)
	client.closeOnce.Do(func() { close(client.closeDone) })
	close(client.controlQueue)
}

func (client *Client) requireConnected() error {
	if err := client.stickyError(); err != nil {
		return err
	}
	client.mu.Lock()
	connected := client.conn != nil && client.connected
	client.mu.Unlock()
	if !connected {
		return &StateError{"client is not connected"}
	}
	return nil
}

func (client *Client) requireOpenContext(
	contextID string,
) (*contextState, error) {
	if err := client.requireConnected(); err != nil {
		return nil, err
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	state := client.contexts[contextID]
	if state == nil {
		return nil, &StateError{"unknown context_id"}
	}
	if state.closing || state.final {
		return nil, &StateError{"context is closing or closed"}
	}
	return state, nil
}

func (client *Client) setSticky(err error) {
	client.mu.Lock()
	if client.sticky == nil {
		client.sticky = err
		for _, state := range client.contexts {
			state.once.Do(func() { close(state.done) })
		}
	}
	client.mu.Unlock()
}

func (client *Client) stickyError() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.sticky
}

func (client *Client) invokeAudio(
	contextID string, audio []byte, alignment map[string]any,
) {
	if client.callbacks.OnAudio == nil {
		return
	}
	defer func() {
		if value := recover(); value != nil {
			client.invokeError(&CallbackError{
				Callback: "OnAudio",
				Cause:    value,
			})
		}
	}()
	client.callbacks.OnAudio(
		contextID, append([]byte(nil), audio...), alignment)
}

func (client *Client) invokeEvent(event ServerEvent) {
	if client.callbacks.OnEvent == nil {
		return
	}
	client.queueControl(func() {
		defer func() {
			if value := recover(); value != nil {
				client.invokeErrorDirect(&CallbackError{
					Callback: "OnEvent",
					Cause:    value,
				})
			}
		}()
		client.callbacks.OnEvent(event)
	}, false)
}

func (client *Client) invokeError(err error) {
	if client.callbacks.OnError == nil {
		return
	}
	client.queueControl(func() { client.invokeErrorDirect(err) }, true)
}

func (client *Client) invokeErrorDirect(err error) {
	defer func() { _ = recover() }()
	client.callbacks.OnError(err)
}

func (client *Client) invokeContextFinal(result ContextResult) {
	if client.callbacks.OnContextFinal == nil {
		return
	}
	result.Audio = append([]byte(nil), result.Audio...)
	client.queueControl(func() {
		defer func() {
			if value := recover(); value != nil {
				client.invokeErrorDirect(&CallbackError{
					Callback: "OnContextFinal",
					Cause:    value,
				})
			}
		}()
		client.callbacks.OnContextFinal(result)
	}, true)
}

func (client *Client) invokeClose(code int, reason string) {
	if client.callbacks.OnClose == nil {
		return
	}
	client.queueControl(func() {
		defer func() {
			if value := recover(); value != nil {
				client.invokeErrorDirect(&CallbackError{
					Callback: "OnClose",
					Cause:    value,
				})
			}
		}()
		client.callbacks.OnClose(code, reason)
	}, true)
}

func (client *Client) queueControl(callback func(), critical bool) {
	defer func() { _ = recover() }()
	job := callbackJob{run: callback, critical: critical}
	select {
	case client.controlQueue <- job:
		return
	default:
	}
	if !critical {
		return
	}
	select {
	case <-client.controlQueue:
	default:
	}
	select {
	case client.controlQueue <- job:
	default:
	}
}

func (client *Client) callbackLoop() {
	for job := range client.controlQueue {
		job.run()
	}
}

func cloneMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
