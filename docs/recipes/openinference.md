# OpenInference

## What you get

- Span kinds from `openinference.span.kind`: model calls, tools, chains and agents as a tree.
- Model, tokens and cost on model calls, with cache read, cache write and reasoning tokens when the instrumentor sends them.
- Prompts and completions as chat messages, tool calls as cards, a tool span's arguments and result.
- Sessions, from `session.id` (`using_session`).
- A failed call as an error with the provider's message and the prompt that was sent.

## Configuration

OpenInference instrumentors attach to a standard OpenTelemetry SDK tracer provider. Use the OTLP **HTTP** exporter:

```python
from openinference.instrumentation import using_session
from openinference.instrumentation.openai import OpenAIInstrumentor
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor

provider = TracerProvider()
provider.add_span_processor(SimpleSpanProcessor(OTLPSpanExporter(endpoint="http://127.0.0.1:4318/v1/traces")))
OpenAIInstrumentor().instrument(tracer_provider=provider)

with using_session("a-session-id"):
    ...  # calls made here are grouped as one session
```

No key or header is needed. Import the exporter from `...proto.http...`, not `...proto.grpc...`: spoor has no gRPC receiver. The other instrumentors (`AnthropicInstrumentor`, `LangChainInstrumentor`, `LlamaIndexInstrumentor`, `OpenAIAgentsInstrumentor`, `CrewAIInstrumentor`) are set up the same way. CrewAI's documentation lists this route (Arize Phoenix) and OpenLIT; its model calls come from `OpenAIInstrumentor`, because CrewAI calls OpenAI through its own client.

## Tested

2026-10-05. The captures are in `testdata/` and the translation and page tests run on them.

| Instrumentor | Version | Library | Captures |
|---|---|---|---|
| `openinference-instrumentation-openai` | 0.1.63 | `openai` 3.24.0 | `openinference-openai-{plain,tool,error}` |
| `openinference-instrumentation-anthropic` | 3.0.1 | `anthropic` 1.11.0 | `openinference-anthropic-{plain,tool,error}` |
| `openinference-instrumentation-langchain` | 0.1.78 | `langchain` 1.4.3 (`create_agent`) | `openinference-langchain-tool` |
| `openinference-instrumentation-llama-index` | 4.5.4 | `llama-index-core` 0.14.25 (`FunctionAgent`) | `openinference-llamaindex-tool` |
| `openinference-instrumentation-openai-agents` | 2.5.2 | `openai-agents` 0.23.1, chat completions | `openinference-openai-agents-tool` |
| `openinference-instrumentation-crewai` with the OpenAI instrumentor | 1.1.20 | `crewai` 1.15.23, `openai` 2.54.0 | `openinference-crewai-workflow` |

## Known gaps

- **The OpenAI Agents instrumentor sends no cached or reasoning token count.** The whole prompt is priced as fresh input, so a cached prompt costs more in spoor than it did.
- **A failed OpenAI call has no model.** The instrumentor puts it only inside `llm.invocation_parameters`, which spoor keeps in metadata.
- **A tool result in an Anthropic conversation is shown with the role `user`**, as Anthropic's API has it.
- **Framework spans carry the framework's own dumps.** LangChain and LlamaIndex chain spans send serialized objects as input and output; spoor shows them as sent.
- **Not captured:** streaming, embeddings, retrievers, `llm.cost.total`.
