package myvocalv3dialogue

import (
	"net/url"
	"testing"
)

func TestURLContainsFixedQueryAndDefaults(t *testing.T) {
	options := DefaultConnectionOptions()
	raw, err := options.BuildURL()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	assertEqual(t, "dedicated_concurrency", query.Get("priority"))
	assertEqual(t, "myvocal_v3", query.Get("model_id"))
	assertEqual(t, "mp3_128", query.Get("format"))
	assertEqual(t, "false", query.Get("timestamps"))
	assertEqual(t, "on", query.Get("text_normalization"))
	assertEqual(t, "true", query.Get("enable_logging"))
	if query.Has("seed") || query.Has("language_code") {
		t.Fatal("optional query values must be omitted")
	}
}

func TestAllFormatsAreAccepted(t *testing.T) {
	formats := []string{
		"mp3_64", "mp3_96", "mp3_128", "mp3_192",
		"pcm_8000", "pcm_16000", "pcm_22050", "pcm_24000",
		"pcm_44100", "ulaw_8000",
	}
	for _, format := range formats {
		options := DefaultConnectionOptions()
		options.Format = format
		if err := options.Validate(); err != nil {
			t.Fatalf("%s: %v", format, err)
		}
	}
}

func TestSeedLanguageStabilityAndContextValidation(t *testing.T) {
	for _, seed := range []int64{1, 4294967295} {
		options := DefaultConnectionOptions()
		options.Seed = &seed
		if err := options.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, seed := range []int64{-1, 0, 4294967296} {
		options := DefaultConnectionOptions()
		options.Seed = &seed
		if err := options.Validate(); err == nil {
			t.Fatalf("seed %d should fail", seed)
		}
	}
	options := DefaultConnectionOptions()
	options.LanguageCode = "EN"
	raw, err := options.BuildURL()
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(raw)
	assertEqual(t, "en", parsed.Query().Get("language_code"))

	if err := (VoiceSettings{Stability: 1.01}).Validate(); err == nil {
		t.Fatal("stability should fail")
	}
	voices := make([]string, 11)
	for i := range voices {
		voices[i] = string(rune('a' + i))
	}
	if err := (ContextOptions{ContextID: "ctx", Voices: voices}).Validate(); err == nil {
		t.Fatal("11 voices should fail")
	}
	context := ContextOptions{
		ContextID: "ctx",
		Voices:    []string{"voice-a"},
		Inputs:    []Input{{Text: "Hello", VoiceID: "voice-b"}},
	}
	if err := context.Validate(); err == nil {
		t.Fatal("unregistered input voice should fail")
	}
}

func TestWireShapeOmitsOptionalValues(t *testing.T) {
	context := ContextOptions{
		ContextID:     "ctx",
		Voices:        []string{"voice-a"},
		VoiceSettings: &VoiceSettings{Stability: 0.5},
		Inputs:        []Input{{Text: "Hello", VoiceID: "voice-a"}},
	}
	value, err := context.Wire()
	if err != nil {
		t.Fatal(err)
	}
	inputs := value["inputs"].([]map[string]any)
	if _, exists := inputs[0]["new_turn"]; exists {
		t.Fatal("false new_turn must be omitted")
	}
}

func assertEqual(t *testing.T, expected, actual string) {
	t.Helper()
	if expected != actual {
		t.Fatalf("expected %q, got %q", expected, actual)
	}
}
