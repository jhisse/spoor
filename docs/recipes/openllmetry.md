# OpenLLMetry (Traceloop SDK)

## What you get

- Model, tokens and cost on model-call spans, with cache read, cache write and reasoning tokens.
- Prompts and completions as chat messages, tool calls as cards.
- Sessions, when you set a `session_id` association property.
- A failed call as an error with the provider's message and the prompt that was sent.

## Configuration

```sh
TRACELOOP_BASE_URL=http://127.0.0.1:4318
```

```python
from traceloop.sdk import Traceloop

Traceloop.init(app_name="my-app")
Traceloop.set_association_properties({"session_id": "a-session-id"})  # optional: groups traces as a session
```

- Keep the `http://` prefix. Per the documentation, a base URL without it makes the SDK use gRPC, which spoor does not accept.
- No key is needed: with the variable above and no `TRACELOOP_API_KEY`, the SDK exports.
- `TRACELOOP_TRACE_CONTENT` (default true) controls whether prompts and completions are put on spans. With it off, spoor shows structure, tokens and cost, but no messages.
- The SDK has its own anonymous telemetry, separate from your traces; `TRACELOOP_TELEMETRY=false` turns it off.

## Tested

2026-10-05, `traceloop-sdk` 0.62.4 with `openai` 3.24.0 and `anthropic` 1.11.0: `traceloop-openai-{plain,tool,error}` and `traceloop-anthropic-{plain,tool}` in `testdata/`. This version sends messages in the OpenTelemetry GenAI form (`gen_ai.input.messages` with `parts`).

The 14 `openllmetry-*` captures use the flattened form (`gen_ai.prompt.N.role`) with real models. Both forms are read, and the translation and page tests run on all of them.

Documentation read on 2026-10-03: <https://www.traceloop.com/docs/openllmetry/configuration>.

## Known gaps

- **No session without the association property.** The SDK sends no conversation id of its own; the older multi-turn captures arrive as separate traces.
- **Not captured with the current version:** streaming and the framework instrumentors (LangChain, LlamaIndex). `opentelemetry-instrumentation-crewai` 0.62.4 with `crewai` 1.15.23 was run on 2026-10-04: it sends the crew, agent and task spans but no model calls, so no tokens or cost. For CrewAI use [OpenInference](openinference.md).
