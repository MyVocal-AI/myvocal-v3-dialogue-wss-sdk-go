# Dialogue SDK usage guide

Use this guide with the language-specific installation and example commands in [README](README.md).

## Connect and choose a model

The SDK connects to:

```text
wss://api.voicelibrary.co/enterprise/v1/text-to-dialogue/websocket/multi
```

Pass your enterprise TTS API Key to the client constructor. Authentication uses the `api-key` handshake header. Keep the Key in server-side configuration or an environment variable; do not include it in a URL, public source code, or logs.

This endpoint requires dedicated concurrency (`priority=dedicated_concurrency`). Choose `myvocal_v3` (default), `myvocal_v4`, or `myvocal_v3_turbo`. The latter two models support this dialogue endpoint, but not ordinary TTS Single or Multi-Context WebSocket endpoints.

## Connection options

| Purpose | Python | Java builder | Go | Default / values |
| --- | --- | --- | --- | --- |
| Model | `model_id` | `modelId(...)` | `ModelID` | `myvocal_v3`; also `myvocal_v4`, `myvocal_v3_turbo` |
| Audio format | `format` | `format(...)` | `Format` | `mp3_128` |
| Language | `language_code` | `languageCode(...)` | `LanguageCode` | Omit for automatic selection; two-letter code such as `en`, `zh` |
| Character timing | `timestamps` | `timestamps(...)` | `Timestamps` | `false` |
| Text normalization | `text_normalization` | `textNormalization(...)` | `TextNormalization` | `on`; `auto`, `on`, `off` |
| Consistent generation | `seed` | `seed(...)` | `Seed` | Optional; integer `1..4294967295` |
| Generation history | `enable_logging` | `enableLogging(...)` | `EnableLogging` | `true`; availability depends on your account |

Start with `mp3_128` and default settings. Other SDK format values are `mp3_64`, `mp3_96`, `mp3_192`, `pcm_8000`, `pcm_16000`, `pcm_22050`, `pcm_24000`, `pcm_44100`, and `ulaw_8000`. Language, advanced options, and format availability depend on the selected model; see the [API reference](https://api-docs.myvocal.ai/3892090w0).

A fixed seed can improve consistency but does not guarantee identical audio. For exact audio separation between speaking turns, prefer PCM; MP3 chunk boundaries may not match turn boundaries exactly.

## Create a context and send text

A context is an independent dialogue within one connection. Register its voices when you create it, then send text using those voice IDs.

| Operation | Python | Java | Go |
| --- | --- | --- | --- |
| Connect | `connect` | `connect` | `Connect` |
| Create context | `open_context` | `openContext` | `OpenContext` |
| Add text | `send_inputs` | `sendInputs` | `SendInputs` |
| Generate buffered text now | `flush` | `flush` | `Flush` |
| Keep context active | `keep_alive` | `keepAlive` | `KeepAlive` |
| Finish one context | `close_context` | `closeContext` | `CloseContext` |
| Finish the connection | `close_socket` | `closeSocket` | `CloseSocket` |
| Cancel immediately | `abort` | `abort` | `Abort` |

- Use a non-empty context ID, unique within the connection.
- A connection supports up to 5 active contexts. Each context registers 1 to 10 distinct voice IDs.
- Set optional voice settings when creating the context. `stability` accepts values from `0` to `1`.
- Each input needs non-empty text and a voice ID registered in that context.
- Setting `new_turn=true`, or changing the voice ID, starts a new speaking turn.
- You may create a context without initial text and send inputs later.
- Do not send more text after requesting context or connection closure.

Creating a context successfully means the request was sent; continue receiving audio and errors to determine its result.

## Receive audio

The audio callback receives decoded audio bytes and the context ID. Save or play chunks in arrival order for each context, using the selected audio format. An audio callback is not a completion signal.

Keep callbacks short. Move file processing, playback work, and other slow tasks to your application's worker or task. Do not wait for a blocking SDK operation from a callback. Optional informational events may be skipped when the application cannot process them quickly; do not use them as your only completion signal.

With `timestamps=true`, audio messages may also include `alignment` and `normalized_alignment`, with `chars`, `char_start_times_ms`, and `char_durations_ms`. Timing values are milliseconds.

The supplied basic example prints received byte counts. To save audio, write the callback bytes to a separate file for each context in arrival order.

## Keep alive, flush, and finish

Call keep-alive before a context reaches its 20-second idle timeout. WebSocket ping/pong does not replace context keep-alive.

Flush requests immediate generation of buffered text and keeps the context open. It does not wait for a separate flush-complete event.

Closing a context processes its remaining text and waits for its `is_final=true`. Other contexts and the connection remain usable. No separate flush is required before normal context closure.

Closing the connection waits for all active contexts to finish and for WebSocket Close Code `1000`. Repeated close calls are safe. A turn-final event (`is_final_audio_for_turn`) ends one speaking turn; it does not close the context.

An immediate abort cancels the connection and does not mean synthesis completed. Use it when cancelling, cleaning up after a failure, or shutting down the application.

## Errors, timeouts, and reconnecting

| Error category | What to do |
| --- | --- |
| Validation | Check option values, text, voice registration, and context limits. |
| Authentication | Check the enterprise API Key and account access. |
| State | Check call order; do not send to a closed or failed context. |
| Server / protocol | Read the error code, message, field, and context ID when present. |
| Transport / timeout | Handle the failed operation and close or abort as appropriate. |
| Callback | Fix the application callback; an already completed synthesis remains completed. |

Python reports SDK exceptions; Java reports SDK exceptions and listener errors; Go returns errors and invokes `OnError`. Error type names follow each language's conventions.

A context-specific error affects that context. A connection-level failure affects all unfinished contexts. A normal close code received after an earlier error does not erase that error. Closing before the required final events is not successful completion.

Use explicit timeouts for connection and close operations. In Python, `connect_timeout` is in seconds and close methods accept `timeout` in seconds. Java wait arguments are milliseconds. Go blocking operations use `context.Context` deadlines. A timeout is an error, not a completion signal; abort the connection if your application is cancelling the operation.

The SDK does not automatically reconnect or replay submitted text. After disconnection, create a new client and decide whether to submit a new request. Repeating completed or partially processed text can produce duplicate audio and additional usage.

## Usage and support

Use [Query primary key usage](https://api-docs.myvocal.ai/336818975e0) to view character usage. For supported fields, message examples, and error codes, see the [dialogue API reference](https://api-docs.myvocal.ai/3892090w0).

For support, provide the SDK version, model, error type/code, and operation that failed. Do not include your API Key or private audio/text in logs shared publicly.
