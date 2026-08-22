# MyVocal V3 Dialogue WebSocket SDK for Go

Go 1.22+ multi-context V3 dialogue client.

## Local Installation

The module is not yet published to GitHub. From an extracted local package,
use a temporary `replace` while developing:

```go
require github.com/MyVocal-AI/myvocal-v3-dialogue-wss-sdk-go v0.0.0

replace github.com/MyVocal-AI/myvocal-v3-dialogue-wss-sdk-go => ../myvocal-v3-dialogue-wss-sdk-go-1.0.1
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
Priority and model are fixed by the SDK.

Use `KeepAlive` before a context reaches its 20-second application idle
timeout. WebSocket ping/pong does not replace this message.

Audio callbacks run directly on the reader and must return quickly. Control
and lifecycle callbacks use a 16-slot non-blocking queue; lifecycle callbacks
take priority during an event storm. Callback panics are reported as
`CallbackError` and do not change a successful protocol result.

A server error with a registered `context_id` terminates only that context.
Connection-level errors remain sticky and are returned by later close calls.

Successfully generated text is charged at twice the standard TTS character
usage.

The live test is skipped unless `MYVOCAL_TTS_API_KEY` and
`MYVOCAL_VOICE_ID` are present in the current process.
