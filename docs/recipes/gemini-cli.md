# Gemini CLI

## What you get

- One trace per model call, named `llm_call`, with the model, input and output tokens, and a cost calculated from spoor's price table.
- The system instructions, the prompt and the answer as chat messages, when detailed traces are on.
- Sessions: every call of one CLI session carries the same `gen_ai.conversation.id`.

## Configuration

```sh
export GEMINI_TELEMETRY_ENABLED=true
export GEMINI_TELEMETRY_OTLP_PROTOCOL=http
export GEMINI_TELEMETRY_OTLP_ENDPOINT=http://127.0.0.1:4318
```

No key or header is needed. For prompts and answers on the spans, also set in `~/.gemini/settings.json`:

```json
{"telemetry": {"traces": true, "logPrompts": true}}
```

## Tested

2026-10-05, Gemini CLI 0.62.0, `gemini --prompt` with an API key and `GOOGLE_GEMINI_BASE_URL` pointed at the fake provider in `testdata/capture/` (no real model was called, no Google login): `gemini-cli-plain` in `testdata/`. Documentation read on the same day: <https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/telemetry.md>.

## Known gaps

- **No cached or thinking token count on the span.** The fake reported 1,024 cached and 32 thinking tokens; the span carried only input 1,200 and output 8. spoor prices the whole prompt as fresh input, so a cached prompt costs more in spoor than it did.
- **Each model call is its own trace.** The capture has one span with no parent; a session's calls are grouped by the conversation id, not by a trace.
- **The prompt is large.** The system instructions and tool definitions are sent on every call (about 39 KB for a one-line question).
- **The resource attributes name your machine and user** (`host.name`, `process.owner`, `process.command_args`). spoor stores them on the trace as sent.
- **Not captured:** tool calls, an interactive session, a login with a Google account.
