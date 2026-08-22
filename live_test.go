package myvocalv3dialogue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"
)

func TestLiveTwoContextsCloseSocket(t *testing.T) {
	apiKey := os.Getenv("MYVOCAL_TTS_API_KEY")
	voiceID := os.Getenv("MYVOCAL_VOICE_ID")
	if apiKey == "" || voiceID == "" {
		t.Skip("live credentials are process-only")
	}

	var lock sync.Mutex
	var audio []byte
	audioChunks := 0
	alignments := 0
	turnFinals := 0
	var callbackErrors []error
	callbacks := Callbacks{
		OnAudio: func(contextID string, value []byte, alignment map[string]any) {
			if contextID != "go-live-a" && contextID != "go-live-b" {
				t.Errorf("unexpected context_id")
			}
			lock.Lock()
			audio = append(audio, value...)
			audioChunks++
			if alignment != nil {
				alignments++
			}
			lock.Unlock()
		},
		OnEvent: func(event ServerEvent) {
			if final, _ := event.Raw["is_final_audio_for_turn"].(bool); final {
				lock.Lock()
				turnFinals++
				lock.Unlock()
			}
		},
		OnError: func(err error) {
			lock.Lock()
			callbackErrors = append(callbackErrors, err)
			lock.Unlock()
		},
	}
	seed := int64(821003)
	options := DefaultConnectionOptions()
	options.Format = "pcm_16000"
	options.LanguageCode = "en"
	options.Timestamps = true
	options.TextNormalization = "auto"
	options.Seed = &seed
	options.EnableLogging = false
	client := NewClient(apiKey, options, callbacks)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id   string
		text string
	}{
		{"go-live-a", "Hello from the first SDK context."},
		{"go-live-b", "Hello from the second SDK context."},
	} {
		settings := &VoiceSettings{Stability: 0.5}
		err := client.OpenContext(ContextOptions{
			ContextID:     item.id,
			Voices:        []string{voiceID},
			VoiceSettings: settings,
			Inputs: []Input{{
				Text: item.text, VoiceID: voiceID, NewTurn: true,
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err := client.CloseSocket(ctx)
	if err != nil {
		t.Fatal(err)
	}

	lock.Lock()
	defer lock.Unlock()
	if result.CloseCode != 1000 ||
		!client.ContextDone("go-live-a") ||
		!client.ContextDone("go-live-b") ||
		audioChunks == 0 || len(audio) == 0 ||
		alignments == 0 || len(callbackErrors) != 0 {
		t.Fatalf("live result did not satisfy contract")
	}
	sum := sha256.Sum256(audio)
	summary := map[string]any{
		"sdk":             "go",
		"contextsFinal":   2,
		"turnFinals":      turnFinals,
		"audioChunks":     audioChunks,
		"audioBytes":      len(audio),
		"audioSha256":     hex.EncodeToString(sum[:]),
		"alignmentEvents": alignments,
		"closeCode":       result.CloseCode,
		"pass":            true,
	}
	value, _ := json.Marshal(summary)
	t.Log(string(value))
}
