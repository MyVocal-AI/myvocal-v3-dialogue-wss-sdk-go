# MyVocal Dialogue WebSocket SDK for Go

Go 1.22+ multi-context dialogue client.

## Model Selection (v1.1.0)

Supports `myvocal_v3` (default), `myvocal_v4`, and `myvocal_v3_turbo`.
Package names and import paths retain `v3` for compatibility.

```go
options := dialogue.DefaultConnectionOptions()
options.ModelID = "myvocal_v4" // Or "myvocal_v3_turbo"
```

Use `DefaultConnectionOptions()` or keyed struct literals. Adding `ModelID`
requires callers using unkeyed `ConnectionOptions` literals to migrate to
keyed fields. An omitted/empty `ModelID` retains `myvocal_v3`.

These models use the **dialogue Multi-Context** endpoint. The two new models
do not support ordinary TTS Single or ordinary TTS Multi-Context WebSocket.
Model IDs are sent unchanged; the SDK does not maintain a model whitelist.
Available SDK options are not a promise that every model supports every
advanced option or format. Start with `mp3_128` and default options.

## Installation

From an existing Go module:

```bash
go get github.com/MyVocal-AI/myvocal-v3-dialogue-wss-sdk-go@v1.1.0
```

## Local ZIP Installation

When using an extracted ZIP instead of GitHub, add a temporary `replace`:

```go
require github.com/MyVocal-AI/myvocal-v3-dialogue-wss-sdk-go v0.0.0

replace github.com/MyVocal-AI/myvocal-v3-dialogue-wss-sdk-go => ../myvocal-v3-dialogue-wss-sdk-go-1.1.0
```

Then run:

```bash
go mod tidy
go test ./...
```

## Lifecycle

```text
Connect -> OpenContext -> SendInputs/Flush/KeepAlive
        -> CloseContext or CloseSocket -> Close 1000
```

Blocking waits accept `context.Context`. Duplicate close calls are idempotent.
The SDK does not reconnect or replay an active session.

Connection options expose the 10 public formats, optional language code,
timestamps, text normalization, seed `1..4294967295`, and logging option.
Priority remains fixed at `dedicated_concurrency`; model selection is configurable.

Use `KeepAlive` before a context reaches its 20-second application idle
timeout. WebSocket ping/pong does not replace this message.

Audio callbacks run directly on the reader and must return quickly. Control
and lifecycle callbacks use a 16-slot non-blocking queue; lifecycle callbacks
take priority during an event storm. Callback panics are reported as
`CallbackError` and do not change a successful protocol result.

A server error with a registered `context_id` terminates only that context.
Connection-level errors remain sticky and are returned by later close calls.

Usage is reported by `GET https://api.voicelibrary.co/enterprise/v1/user/usage`.
`myvocal_v3`, `myvocal_v4`, and `myvocal_v3_turbo` record characters under
`tts.v3_chars`, `tts.v4_chars`, and `tts.v3_turbo_chars`, respectively,
and contribute to `tts.total_chars`. For usage details, see
[Query primary key usage](https://api-docs.myvocal.ai/336818975e0).

The live test is skipped unless `MYVOCAL_TTS_API_KEY` and
`MYVOCAL_VOICE_ID` are present in the current process.

The packaged basic example also accepts optional `MYVOCAL_MODEL_ID`.
