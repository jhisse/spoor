# OpenTelemetry's OpenAI instrumentation

`opentelemetry-instrumentation-openai-v2`, the instrumentor maintained in the OpenTelemetry Python contrib repository.

## What you get

- One `chat <model>` span per call, with the model, input and output tokens, and a cost calculated from spoor's price table.
- Prompts and completions as chat messages and tool calls as cards, **only in `span_only` or `span_and_event` mode** (below).
- A failed call as an error with the provider's message.

## Configuration

```sh
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
export OTEL_SEMCONV_STABILITY_OPT_IN=gen_ai_latest_experimental
export OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=span_only
```

```python
from opentelemetry.instrumentation.openai_v2 import OpenAIInstrumentor

OpenAIInstrumentor().instrument()
```

No key or header is needed. The last two variables are what put the messages on the span. With `event_only` they are sent as log records, and without the variable they are not sent at all; spoor stores traces only, so either way it shows tokens and cost and no text.

## Tested

2026-10-05, `opentelemetry-instrumentation-openai-v2` 2.4b0 with `opentelemetry-util-genai` 1.1b0 and `openai` 3.24.0: `otel-openai-v2-{tool,events,error}` in `testdata/`. `-events` is the `event_only` mode: the sink received the log record, and the span has no content.

## Known gaps

- **No cached or reasoning token count on the span.** The whole prompt is priced as fresh input, so a cached prompt costs more in spoor than it did.
- **`event_only` content is lost.** spoor answers `POST /v1/logs` with success and stores nothing.
- **Version 2.4b0 does not import with `opentelemetry-util-genai` 1.2b0**, the version a plain install resolves to (`No module named 'opentelemetry.util.genai.instruments'`). Pin 1.1b0.
- **No session.** The instrumentor sends no conversation id.
- **Not captured:** streaming, the Responses API, embeddings.
