# MyVocal Dialogue WebSocket SDK for Go

Go client for multi-role dialogue synthesis. Requires Go 1.22 or later and an enterprise TTS API Key with dedicated concurrency.

## Install

From your application's Go module:

```bash
go get github.com/MyVocal-AI/myvocal-v3-dialogue-wss-sdk-go@v1.1.2
```

To use the [downloadable ZIP](https://github.com/MyVocal-AI/myvocal-v3-dialogue-wss-sdk-go/releases/tag/v1.1.2), extract it beside your application and add these entries to your application's `go.mod`:

```go
require github.com/MyVocal-AI/myvocal-v3-dialogue-wss-sdk-go v1.1.2

replace github.com/MyVocal-AI/myvocal-v3-dialogue-wss-sdk-go => ../myvocal-v3-dialogue-wss-sdk-go-1.1.2
```

Adjust the relative path to your extracted folder, then run `go mod tidy`.

## Choose a model

Supported models: `myvocal_v3` (default), `myvocal_v4`, and `myvocal_v3_turbo`.
Use these SDKs with the dialogue Multi-Context WebSocket endpoint. `myvocal_v4` and `myvocal_v3_turbo` do not support ordinary TTS WebSocket endpoints.

```go
import dialogue "github.com/MyVocal-AI/myvocal-v3-dialogue-wss-sdk-go"

options := dialogue.DefaultConnectionOptions()
options.ModelID = "myvocal_v4" // Or "myvocal_v3_turbo"
```

Use `DefaultConnectionOptions()` or named fields when constructing options. An omitted or empty `ModelID` selects `myvocal_v3`.

## Output languages

`myvocal_v4` and `myvocal_v3_turbo` support the same 94 output languages. Use `zh` for Mandarin Chinese and `yue` for Cantonese. See the [language list](https://api-docs.myvocal.ai/64249845f0#输出语言) for all supported codes.

```go
options := dialogue.DefaultConnectionOptions()
options.ModelID = "myvocal_v4"
options.LanguageCode = "yue"
```

Omit the language option to select a language from the input text. Provide text in the desired language; the option does not translate it.

## Run the example

Set the following environment variables before running the example:

```bash
export MYVOCAL_TTS_API_KEY='YOUR_API_KEY'
export MYVOCAL_VOICE_ID='YOUR_VOICE_ID'
export MYVOCAL_MODEL_ID='myvocal_v4'
```

PowerShell:

```powershell
$env:MYVOCAL_TTS_API_KEY='YOUR_API_KEY'
$env:MYVOCAL_VOICE_ID='YOUR_VOICE_ID'
$env:MYVOCAL_MODEL_ID='myvocal_v4'
```

`MYVOCAL_MODEL_ID` is optional; the default is `myvocal_v3`. Running the example makes a real synthesis request and consumes character usage. It prints audio byte counts and the connection close code.

From the extracted SDK folder:

```bash
go run ./examples/basic
```

The [complete example](examples/basic/main.go) shows audio callbacks, two contexts, and a one-minute deadline using `context.Context`.

## Use the SDK in your application

- [Usage guide](USAGE.md): connection options, contexts, audio callbacks, completion, errors, and timeouts.
- [API reference](https://api-docs.myvocal.ai/3892090w0): messages, model support, and limits.
- [Character usage](https://api-docs.myvocal.ai/336818975e0).

Keep callbacks short and handle audio separately for each context. Wait for context final events and Close Code `1000` when finishing the connection. The SDK does not automatically reconnect or replay text.

Licensed under [Apache License 2.0](LICENSE). Dependency notices are in [NOTICE](NOTICE).
