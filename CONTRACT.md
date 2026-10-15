# MyVocal Dialogue Multi-Context WebSocket SDK Contract

Version: `1.1.0`

Status: Frozen for Python, Java, and Go implementation.

This document is the normative cross-language contract. Language APIs may be
idiomatic, but wire fields, defaults, validation, state transitions, errors,
completion semantics, and security behavior must remain equivalent.

## 1. Endpoint And Authentication

```text
wss://api.voicelibrary.co/enterprise/v1/text-to-dialogue/websocket/multi
```

- Authentication uses the WebSocket handshake Header `api-key`.
- The API Key is supplied by the application at runtime.
- The SDK must not log, persist, hash, partially reveal, or serialize the Key.
- TLS certificate and hostname verification must remain enabled.

The SDK always sends this fixed Query value:

```text
priority=dedicated_concurrency
```

Applications cannot replace priority. `model_id` is configurable and defaults
to `myvocal_v3`; `myvocal_v4` and `myvocal_v3_turbo` are also supported.
Model IDs are passed unchanged without a client model whitelist.
These new models require this dialogue endpoint, not ordinary TTS WSS.

## 2. Connection Options

| SDK option | Wire field | Type | Default | Validation |
|---|---|---|---|---|
| modelId | `model_id` | string | `myvocal_v3` | Sent unchanged; server validates support |
| format | `format` | enum | `mp3_128` | One of the 10 public formats |
| languageCode | `language_code` | string or absent | absent | Two ISO 639-1 ASCII letters; serialize lowercase |
| timestamps | `timestamps` | boolean | `false` | Boolean |
| textNormalization | `text_normalization` | enum | `on` | `auto`, `on`, or `off` |
| seed | `seed` | unsigned integer or absent | absent | `1` through `4294967295`; `0` is rejected |
| enableLogging | `enable_logging` | boolean | `true` | Boolean |
| connectTimeout | client only | duration | 30 seconds | Greater than zero |
| closeTimeout | client only | duration | 60 seconds | Greater than zero |

SDK-accepted formats (model-specific availability may differ):

```text
mp3_64
mp3_96
mp3_128
mp3_192
pcm_8000
pcm_16000
pcm_22050
pcm_24000
pcm_44100
ulaw_8000
```

Optional Query fields are omitted only when the contract says `absent`.
Booleans and enum defaults are serialized explicitly for identical behavior
across languages.

## 3. JSON Wire Rules

- All client messages are UTF-8 JSON sent in WebSocket text frames.
- Wire names use `snake_case`.
- Unknown application fields are rejected locally and are never forwarded.
- Optional fields are omitted rather than serialized as `null`.
- The SDK may defensively parse a UTF-8 JSON server message received in a
  binary frame, but it never sends binary JSON.
- Malformed JSON from the server is a protocol failure.

## 4. Context Creation

Conceptual API:

```text
openContext(contextId, voices, optional voiceSettings, optional inputs)
```

Wire shape:

```json
{
  "context_id": "scene-001",
  "voices": ["VOICE_A", "VOICE_B"],
  "voice_settings": {
    "stability": 0.5
  },
  "inputs": [
    {
      "text": "Hello.",
      "voice_id": "VOICE_A",
      "new_turn": true
    }
  ]
}
```

Validation:

- `context_id` must be a non-empty string.
- A context ID cannot be opened twice on the same connection.
- At most 5 contexts may be active at once.
- `voices` contains 1 through 10 non-empty voice IDs.
- The SDK does not persist or log voice IDs.
- `voice_settings` is allowed only in the first context message.
- `voice_settings.stability` is optional and must be between `0` and `1`,
  inclusive.
- `inputs` may be omitted during context creation.

`openContext` succeeds after local validation and successful WebSocket write.
The server does not provide a separate context-open acknowledgement.

## 5. Sending Text

Conceptual API:

```text
sendInputs(contextId, inputs)
```

Wire shape:

```json
{
  "context_id": "scene-001",
  "inputs": [
    {
      "text": "How may I help you?",
      "voice_id": "VOICE_A",
      "new_turn": false
    }
  ]
}
```

Validation:

- The context must be active.
- `inputs` contains at least one item.
- `text` must be a non-empty string. The SDK does not invent an unverified
  maximum length.
- `voice_id` must be one of the voices registered for that context.
- `new_turn` defaults to `false` and is serialized only when `true`.
- Changing `voice_id` or setting `new_turn=true` begins a new turn.
- Sending after context final, context close request, socket close request, or
  terminal connection failure raises a state error.

## 6. Control Operations

### Flush

```json
{"context_id": "scene-001", "flush": true}
```

`flush(contextId)` writes the control message and returns. It does not wait for
a dedicated acknowledgement because the protocol has no flush-complete event.
The context remains active.

### Keep Alive

```json
{"context_id": "scene-001", "keep_alive": true}
```

`keepAlive(contextId)` resets the context's 20-second application idle timer.
WebSocket ping/pong does not replace this message.

### Close Context

```json
{"context_id": "scene-001", "close_context": true}
```

`closeContext(contextId)` sends the message and waits for that context's
`is_final=true`. It does not close other contexts or the WebSocket.

Calling `closeContext` again for a closing or completed context is idempotent
and returns the existing completion result.

### Close Socket

```json
{"close_socket": true}
```

`closeSocket()` stops new writes, requests all active contexts to finish, waits
for each active context's `is_final=true`, and then waits for WebSocket Close
Code `1000`.

Calling `closeSocket` more than once is idempotent.

### Abort

`abort()` closes the transport immediately and makes no success claim. It is
used for cancellation, timeout cleanup, or application shutdown.

## 7. Server Events

### Audio

```json
{
  "audio": "<BASE64_AUDIO>",
  "context_id": "scene-001"
}
```

- Decode Base64 strictly.
- Preserve arrival order independently for each context.
- Audio is delivered directly to the audio callback and never placed in a
  control-event queue.
- Per-context audio accumulation is optional and disabled by default.
- When accumulation is enabled, `ContextResult.audio` contains ordered bytes.

### Turn Final

```json
{
  "context_id": "scene-001",
  "is_final_audio_for_turn": true
}
```

This completes one turn only. It does not complete or close the context.

### Context Final

```json
{
  "context_id": "scene-001",
  "is_final": true
}
```

This is the only successful context-completion event. It releases one local
active-context slot.

### Alignment

When timestamps are enabled, an audio event may contain `alignment` and
`normalized_alignment`. Each object may contain:

```text
chars: string[]
char_start_times_ms: number[]
char_durations_ms: number[]
```

The SDK preserves these arrays without changing character segmentation.

### Error

Server errors may use:

```text
code
error
field
message
context_id
```

The first terminal server error is sticky and cannot be replaced by a later
Close Frame or transport error.

## 8. State Model

Connection states:

```text
NEW -> CONNECTING -> OPEN -> CLOSING -> CLOSED
                       \-> FAILED
```

Context states:

```text
OPENING -> ACTIVE -> CLOSING -> COMPLETED
                  \-> FAILED
```

Rules:

- One reader loop owns server-frame parsing.
- All writes are serialized through one connection-level write lock.
- Context state is isolated by `context_id`.
- A failure scoped to one context fails that context. A connection-level
  protocol or transport failure fails every unfinished context.
- A completed context remains immutable.
- Public send methods reject invalid state before writing.

## 9. Success And Failure

Context success:

```text
is_final == true for that context
AND no sticky terminal error for that context
```

Graceful socket success:

```text
all contexts active when close_socket was sent received is_final == true
AND no sticky connection-level terminal error
AND websocket close_code == 1000
```

`is_final_audio_for_turn` is never sufficient for context or socket success.
Receiving audio is never sufficient for success.

Error categories:

```text
ValidationError
AuthenticationError
StateError
ProtocolError
TransportError
TimeoutError
CallbackError
```

Language implementations may use idiomatic class/type names but must preserve
the category, server `code` or `error`, `field`, `message`, context ID, and
Close Code when available.

Priority:

1. First terminal server protocol error.
2. Authentication or validation failure.
3. Timeout or transport failure.
4. Close status.

A later Close `1000` cannot convert a prior terminal error into success. A
Close `1008` without an earlier JSON error is a protocol error. An unexpected
Close `1000` before required context finals is an incomplete-protocol error.

## 10. Callbacks, Queues, And Concurrency

Conceptual callbacks:

```text
onAudio(contextId, bytes, optional alignment)
onEvent(event)
onContextFinal(contextId, result)
onError(error)
onClose(closeCode, reason)
```

- Callbacks preserve reader order and must return quickly.
- Callback exceptions or panics are isolated and reported as `CallbackError`.
- A callback failure does not change an already successful protocol result.
- Callbacks must not call blocking client operations on the reader thread.
- Control-event delivery is bounded and non-blocking.
- Repeated control events cannot block the reader loop.
- The control queue holds at most 16 callbacks. When full, a new ordinary
  event may be dropped; lifecycle callbacks (`onError`, `onContextFinal`, and
  `onClose`) take precedence over queued ordinary events.
- Applications perform expensive work in their own executors or goroutines.

## 11. Reconnect And Retry

- The SDK does not automatically reconnect an active synthesis session.
- The SDK never replays text, context creation, flush, or close messages.
- A bounded handshake retry may be offered only before any application message
  has been written.
- After a disconnect, the application creates a new client and decides what,
  if anything, should be sent again.

## 12. Timeouts And Cleanup

- Connect timeout cancels the pending socket.
- Context-close timeout fails that wait and aborts only when the caller asks
  for connection cancellation; it must not silently mark the context complete.
- Socket-close timeout aborts the transport and returns `TimeoutError`.
- Reader, writer, scheduler, and callback resources are released on terminal
  close.
- Local `close` or `abort` is idempotent.

## 13. Cross-Language Public Surface

Equivalent concepts must exist in Python, Java, and Go:

```text
Client
ConnectionOptions
ContextOptions
VoiceSettings
Input
Alignment
Event
ContextResult
CloseResult
SDKError hierarchy/category
```

Equivalent operations:

```text
connect
open_context
send_inputs
flush
keep_alive
close_context
close_socket
abort/close
```

Names may follow language conventions, but serialization and behavior cannot
diverge.

## 14. Runtime And Packaging Baseline

- Python: 3.9 or later; async API; wheel and source package.
- Java: compile with `--release 8`, class major `52`; test Java 8, 11, 17,
  21, and 25; thin Jar dependencies documented.
- Go: 1.22 or later; context-aware blocking operations.
- Apache License 2.0 plus NOTICE in every source tree and ZIP.
- Live tests run only when explicit environment variables are present.

## 15. Required Tests

All three implementations must test:

- URL construction, fixed fields, omission, lowercase language code, and URL
  encoding.
- All 10 formats and invalid formats.
- seed absent, `1`, `4294967295`, `0`, negative, and overflow.
- context and voice count boundaries.
- stability boundaries.
- registered and unregistered input voice IDs.
- message serialization for open, inputs, flush, keep-alive, context close,
  and socket close.
- text and defensive binary server JSON parsing.
- Base64 decode and per-context audio ordering.
- turn final versus context final.
- alignment parsing.
- first terminal error remains sticky.
- callback throw or panic isolation.
- bounded control-event storm.
- connect, context-close, and socket-close timeouts.
- duplicate context close and duplicate socket close.
- send after close/final.
- unexpected Close `1000`, policy Close `1008`, and transport failure.
- one real full-field synthesis with two contexts when credentials are
  explicitly provided.

## 16. Security And Public-Surface Rules

- No API Key, voice ID, audio, raw frame, cookie, session, HAR, or internal
  endpoint is committed or packaged.
- Examples load the Key and voice IDs from environment variables.
- Logs redact request Headers and user text by default.
- Public documentation and source do not name upstream providers or internal
  field mappings.
- Only fields in this contract are accepted by public request builders.

## 17. Usage Reporting

Usage is reported by `GET https://api.voicelibrary.co/enterprise/v1/user/usage`.
`myvocal_v3`, `myvocal_v4`, and `myvocal_v3_turbo` record characters under
`tts.v3_chars`, `tts.v4_chars`, and `tts.v3_turbo_chars`, respectively,
and contribute to `tts.total_chars`. Use the returned counters to reconcile
usage; no SDK-side multiplier is applied. New-model counters describe new
requests, not a historical backfill.
