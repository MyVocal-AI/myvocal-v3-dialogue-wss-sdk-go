package main

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	dialogue "github.com/MyVocal-AI/myvocal-v3-dialogue-wss-sdk-go"
)

func main() {
	apiKey := required("MYVOCAL_TTS_API_KEY")
	voiceID := required("MYVOCAL_VOICE_ID")
	var totalAudio int64
	options := dialogue.DefaultConnectionOptions()
	if modelID := os.Getenv("MYVOCAL_MODEL_ID"); modelID != "" {
		options.ModelID = modelID
	}
	options.Format = "mp3_128"
	options.LanguageCode = "en"
	options.Timestamps = true
	client := dialogue.NewClient(apiKey, options, dialogue.Callbacks{
		OnAudio: func(contextID string, audio []byte, _ map[string]any) {
			atomic.AddInt64(&totalAudio, int64(len(audio)))
			fmt.Println("audio", contextID, len(audio))
		},
		OnError: func(err error) {
			fmt.Println("error", err)
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	must(client.Connect(ctx))
	settings := &dialogue.VoiceSettings{Stability: 0.5}
	must(client.OpenContext(dialogue.ContextOptions{
		ContextID:     "example-a",
		Voices:        []string{voiceID},
		VoiceSettings: settings,
		Inputs: []dialogue.Input{{
			Text: "Hello from the first context.", VoiceID: voiceID, NewTurn: true,
		}},
	}))
	must(client.OpenContext(dialogue.ContextOptions{
		ContextID:     "example-b",
		Voices:        []string{voiceID},
		VoiceSettings: settings,
		Inputs: []dialogue.Input{{
			Text: "Hello from the second context.", VoiceID: voiceID, NewTurn: true,
		}},
	}))
	result, err := client.CloseSocket(ctx)
	must(err)
	fmt.Println(
		"close", result.CloseCode,
		"audio_bytes", atomic.LoadInt64(&totalAudio),
	)
}

func required(name string) string {
	value := os.Getenv(name)
	if value == "" {
		panic(name + " is required")
	}
	return value
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
