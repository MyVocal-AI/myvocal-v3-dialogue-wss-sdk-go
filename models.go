package myvocalv3dialogue

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const DefaultEndpoint = "wss://api.voicelibrary.co/enterprise/v1/text-to-dialogue/websocket/multi"

var publicFormats = map[string]struct{}{
	"mp3_64": {}, "mp3_96": {}, "mp3_128": {}, "mp3_192": {},
	"pcm_8000": {}, "pcm_16000": {}, "pcm_22050": {},
	"pcm_24000": {}, "pcm_44100": {}, "ulaw_8000": {},
}

var languagePattern = regexp.MustCompile(`^[A-Za-z]{2}$`)

type ConnectionOptions struct {
	Endpoint          string
	Format            string
	LanguageCode      string
	Timestamps        bool
	TextNormalization string
	Seed              *int64
	EnableLogging     bool
	ModelID           string
}

func DefaultConnectionOptions() ConnectionOptions {
	return ConnectionOptions{
		Endpoint:          DefaultEndpoint,
		ModelID:           "myvocal_v3",
		Format:            "mp3_128",
		TextNormalization: "on",
		EnableLogging:     true,
	}
}

func (options ConnectionOptions) Validate() error {
	if options.Endpoint == "" {
		return &ValidationError{"endpoint must not be empty"}
	}
	if _, ok := publicFormats[options.Format]; !ok {
		return &ValidationError{"unsupported format"}
	}
	if options.LanguageCode != "" &&
		!languagePattern.MatchString(options.LanguageCode) {
		return &ValidationError{"language_code must be ISO 639-1"}
	}
	switch options.TextNormalization {
	case "auto", "on", "off":
	default:
		return &ValidationError{"unsupported text_normalization"}
	}
	if options.Seed != nil &&
		(*options.Seed < 1 || *options.Seed > 4294967295) {
		return &ValidationError{"seed must be between 1 and 4294967295"}
	}
	return nil
}

func (options ConnectionOptions) BuildURL() (string, error) {
	if err := options.Validate(); err != nil {
		return "", err
	}
	parsed, err := url.Parse(options.Endpoint)
	if err != nil {
		return "", &ValidationError{"invalid endpoint"}
	}
	query := parsed.Query()
	query.Set("priority", "dedicated_concurrency")
	modelID := options.ModelID
	if modelID == "" {
		modelID = "myvocal_v3"
	}
	query.Set("model_id", modelID)
	query.Set("format", options.Format)
	query.Set("timestamps", strconv.FormatBool(options.Timestamps))
	query.Set("text_normalization", options.TextNormalization)
	query.Set("enable_logging", strconv.FormatBool(options.EnableLogging))
	if options.LanguageCode != "" {
		query.Set("language_code", strings.ToLower(options.LanguageCode))
	}
	if options.Seed != nil {
		query.Set("seed", strconv.FormatInt(*options.Seed, 10))
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

type VoiceSettings struct {
	Stability float64 `json:"stability"`
}

func (settings VoiceSettings) Validate() error {
	if settings.Stability < 0 || settings.Stability > 1 {
		return &ValidationError{"stability must be between 0 and 1"}
	}
	return nil
}

type Input struct {
	Text    string
	VoiceID string
	NewTurn bool
}

func (input Input) validate() error {
	if input.Text == "" {
		return &ValidationError{"input text must not be empty"}
	}
	if input.VoiceID == "" {
		return &ValidationError{"voice_id must not be empty"}
	}
	return nil
}

func (input Input) wire() map[string]any {
	value := map[string]any{
		"text":     input.Text,
		"voice_id": input.VoiceID,
	}
	if input.NewTurn {
		value["new_turn"] = true
	}
	return value
}

type ContextOptions struct {
	ContextID     string
	Voices        []string
	VoiceSettings *VoiceSettings
	Inputs        []Input
}

func (options ContextOptions) Validate() error {
	if options.ContextID == "" {
		return &ValidationError{"context_id must not be empty"}
	}
	if len(options.Voices) < 1 || len(options.Voices) > 10 {
		return &ValidationError{
			"voices must contain between 1 and 10 items",
		}
	}
	registered := make(map[string]struct{}, len(options.Voices))
	for _, voice := range options.Voices {
		if voice == "" {
			return &ValidationError{"voice ids must not be empty"}
		}
		if _, exists := registered[voice]; exists {
			return &ValidationError{"voice ids must be unique"}
		}
		registered[voice] = struct{}{}
	}
	if options.VoiceSettings != nil {
		if err := options.VoiceSettings.Validate(); err != nil {
			return err
		}
	}
	for _, input := range options.Inputs {
		if err := input.validate(); err != nil {
			return err
		}
		if _, exists := registered[input.VoiceID]; !exists {
			return &ValidationError{
				"input voice_id must be registered",
			}
		}
	}
	return nil
}

func (options ContextOptions) Wire() (map[string]any, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	value := map[string]any{
		"context_id": options.ContextID,
		"voices":     append([]string(nil), options.Voices...),
	}
	if options.VoiceSettings != nil {
		value["voice_settings"] = map[string]any{
			"stability": options.VoiceSettings.Stability,
		}
	}
	if len(options.Inputs) > 0 {
		inputs := make([]map[string]any, 0, len(options.Inputs))
		for _, input := range options.Inputs {
			inputs = append(inputs, input.wire())
		}
		value["inputs"] = inputs
	}
	return value, nil
}

type ServerEvent struct {
	Raw map[string]any
}

func (event ServerEvent) ContextID() string {
	value, _ := event.Raw["context_id"].(string)
	return value
}

type ContextResult struct {
	ContextID string
	Audio     []byte
}

type CloseResult struct {
	CloseCode int
}

func protocolErrorFrom(event map[string]any) *ProtocolError {
	code := stringValue(event["code"])
	if code == "" {
		code = stringValue(event["error_code"])
	}
	nested, _ := event["error"].(map[string]any)
	if code == "" && nested != nil {
		code = stringValue(nested["code"])
	}
	if code == "" {
		if value, ok := event["error"].(string); ok {
			code = value
		}
	}
	if code == "" {
		return nil
	}
	message := stringValue(event["message"])
	if message == "" {
		message = stringValue(event["status_msg"])
	}
	field := stringValue(event["field"])
	if nested != nil {
		if message == "" {
			message = stringValue(nested["message"])
		}
		if field == "" {
			field = stringValue(nested["field"])
		}
	}
	if message == "" {
		message = code
	}
	return &ProtocolError{
		Code:      code,
		Field:     field,
		Message:   message,
		ContextID: stringValue(event["context_id"]),
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if result, ok := value.(string); ok {
		return result
	}
	return fmt.Sprint(value)
}
