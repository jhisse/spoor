# The captures

51 OTLP trace exports, each as the bytes a sender posted (`.pb`, an `ExportTraceServiceRequest`) and the same request decoded (`.json`, OTLP/JSON, for reading and for `grep`). spoor's translation and page tests run on them, and `spoor demo` loads the ones `embed.go` names.

They are useful outside spoor too: each file is what one version of one instrumentation really puts on the wire, and the dialects disagree on the names of the model, the token counts, the cache counts and the messages. MIT, like the rest of the repository ([LICENSE](../LICENSE)).

## Where they come from

| Files | Made with | Model |
|---|---|---|
| `claude-code-simple`, `claude-code-tool-read` | Claude Code 2.1.287, `claude -p` | real |
| `claude-code-interactive-*` | interactive Claude Code turns rebuilt from stored rows, every free-text value replaced. Not wire captures | real |
| `openllmetry-*` | OpenLLMetry 0.36.1 (the flattened `gen_ai.prompt.N.*` form) | real, Anthropic and OpenRouter |
| `chatbot-*` | the bots in [`chatbots/`](chatbots/README.md), run by `capture/chatbots.sh` | real, through OpenRouter |
| everything else | one script per SDK in [`capture/`](capture/README.md) | a fake provider that always reports the same usage, so a cost can be checked by hand |

The capture sink replaces the home directory in every string and drops the `host.*` and `process.*` resource attributes.

## What each one is

One scenario is one trace. `plain` is a single call, `tool` a call that uses a tool and the call after it, `error` a call the provider refuses.

| Capture | Instrumentation, as the scope names it | Model | Spans |
|---|---|---|---|
| `claude-code-simple` | `com.anthropic.claude_code.tracing` 1.0.0 | `claude-haiku-4-5-20251001` | 3 |
| `claude-code-tool-read` | the same | `claude-haiku-4-5-20251001` | 7 |
| `claude-code-interactive-tools` | the same | `claude-opus-5-5[1m]`, `claude-sonnet-5[1m]`, `claude-haiku-4-5-20251001` | 20 |
| `claude-code-interactive-parallel` | the same | `claude-opus-5-5[1m]`, `claude-sonnet-5[1m]` | 17 |
| `openinference-openai-{plain,tool,error}` | `openinference.instrumentation.openai` 0.1.63 | `gpt-4o-mini` | 1, 3, 1 |
| `openinference-anthropic-{plain,tool,error}` | `openinference.instrumentation.anthropic` 3.0.1 | `claude-haiku-4-5` | 1, 3, 1 |
| `openinference-langchain-tool` | `openinference.instrumentation.langchain` 0.1.78 | `gpt-4o-mini` | 7 |
| `openinference-llamaindex-tool` | `openinference.instrumentation.llama_index` 4.5.4 | `gpt-4o-mini` | 15 |
| `openinference-openai-agents-tool` | `openinference.instrumentation.openai_agents` 2.5.2 | `gpt-4o-mini` | 8 |
| `openinference-crewai-workflow` | `openinference.instrumentation.crewai` 1.1.20 with `openinference.instrumentation.openai` 0.1.63 | `gpt-4o-mini` | 5 |
| `traceloop-openai-{plain,tool,error}` | `opentelemetry.instrumentation.openai.v1` 0.62.4 (OpenLLMetry) | `gpt-4o-mini` | 1, 3, 1 |
| `traceloop-anthropic-{plain,tool}` | `opentelemetry.instrumentation.anthropic` 0.62.4 (OpenLLMetry) | `claude-haiku-4-5` | 1, 3 |
| `otel-openai-v2-{tool,events,error}` | `opentelemetry.util.genai.handler` 1.1b0 (OpenTelemetry's OpenAI instrumentation 2.4b0) | `gpt-4o-mini` | 3, 1, 1 |
| `vercel-ai-tool` | `gen_ai` (Vercel AI SDK 7.0.127, `@ai-sdk/otel` 1.0.127) | `gpt-4o-mini` | 6 |
| `pydantic-ai-tool` | `pydantic-ai` 2.54.0 | `gpt-4o-mini` | 4 |
| `litellm-otel-tool` | `litellm` (1.104.0, the `otel` callback) | `gpt-4o-mini` | 2 |
| `gemini-cli-plain` | `gemini-cli` (0.62.0) | `gemini-3.6-flash` | 1 |
| `openllmetry-anthropic-{simple,sonnet-simple,tool-use,tool-use-2,multiturn-1,multiturn-2}` | `opentelemetry.instrumentation.anthropic` 0.36.1 | `claude-haiku-4-5`, `claude-sonnet-5` | 1 each |
| `openllmetry-langgraph-tool-loop` | the same | `claude-haiku-4-5` | 3 |
| `openllmetry-openai-openrouter-{simple,tool-use,tool-search,multiturn-1,multiturn-2}` | `opentelemetry.instrumentation.openai.v1` 0.36.1 | `openai/gpt-4o-mini` | 1 each |
| `openllmetry-openrouter-gemini-simple` | the same | `google/gemini-3.6-flash` | 1 |
| `openllmetry-llamaindex-rag-query` | the same | `openai/gpt-4o-mini` | 1 |
| `chatbot-langchain` | `openinference.instrumentation.langchain` 0.1.78 | `anthropic/claude-haiku-4.5` | 12 |
| `chatbot-llamaindex`, `-reasoning` | `opentelemetry.instrumentation.openai.v1` 0.62.4 with `traceloop.tracer` | `gpt-4o-mini`, `gpt-5-mini` | 4, 3 |
| `chatbot-openai-agents` | `openinference.instrumentation.openai_agents` 2.5.2 | `openai/gpt-4o-mini` | 14 |
| `chatbot-plain-anthropic`, `-thinking` | `opentelemetry.instrumentation.anthropic` 0.62.4 with hand-written spans | `anthropic/claude-haiku-4.5` | 5 each |
| `chatbot-plain-openai`, `-reasoning` | `opentelemetry.util.genai.handler` 1.1b0 with hand-written spans | `openai/gpt-4o-mini`, `openai/gpt-5-mini` | 5 each |
| `chatbot-pydanticai`, `-reasoning` | `pydantic-ai` 2.54.0 | `openai/gpt-4o-mini`, `openai/gpt-5-mini` | 5 each |
| `chatbot-smolagents` | `openinference.instrumentation.smolagents` 0.1.42 | `openai/gpt-4o-mini` | 8 |

Which of them carry cache counts is in [docs/recipes](../docs/recipes/README.md#cache-counts); what spoor reads from each is in the recipe of its sender.

## Using one

```sh
curl -X POST http://127.0.0.1:4318/v1/traces -H "Content-Type: application/x-protobuf" --data-binary @pydantic-ai-tool.pb
curl -X POST http://127.0.0.1:4318/v1/traces -H "Content-Type: application/json" --data-binary @pydantic-ai-tool.json
```

Either works against any OTLP/HTTP receiver. The spans keep the date of the capture, so in spoor they are listed at that date, and posting a file twice changes nothing.

## Adding one

[`capture/README.md`](capture/README.md) says how. A capture kept here gets a row in the table above, in `internal/otlp/sdkcaptures_test.go` and in `internal/web/goldencost_test.go`.
