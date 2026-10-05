# Pydantic AI

## What you get

- One trace per agent run: an `invoke_agent <name>` root, with its model calls (`chat <model>`) and tool calls (`execute_tool <tool>`) under it.
- Model, tokens and cost on model calls, with cache read and reasoning tokens.
- Prompts and completions as chat messages; a tool span's arguments and result.
- Sessions: every span of a run carries `gen_ai.conversation.id`.

## Configuration

Pydantic AI uses the global OpenTelemetry tracer provider. No Logfire account is needed.

```python
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from pydantic_ai import Agent

provider = TracerProvider()
provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter(endpoint="http://127.0.0.1:4318/v1/traces")))
trace.set_tracer_provider(provider)
Agent.instrument_all()
```

No key or header is needed.

## Tested

2026-10-05, `pydantic-ai-slim[openai]` 2.54.0 with `OpenAIChatModel`: `pydantic-ai-tool` in `testdata/`. It is one of the traces `spoor demo` shows.

## Known gaps

- **spoor shows its own cost, not Pydantic AI's.** The SDK puts its estimate in `operation.cost`, which stays in metadata; in the capture the two are equal.
- **The conversation id is new on every run** unless you pass one, so each run is its own session.
- **Not captured:** streaming, the Responses API, other providers, a failed run.
