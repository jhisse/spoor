# Recipes

One page per sender: what you get, the configuration, what was tested, the known gaps.

## Tested with

Every row was run on 2026-10-05, except Claude Code (2026-10-03), and has a capture in `testdata/`, except Copilot Chat in VS Code (read from stored rows of a real session). The model was the fake provider in `testdata/capture/`, which is also how to add a row, except for Claude Code and the `openllmetry-*` captures (real models). The eleven `chatbot-*` captures (real models through OpenRouter, `testdata/capture/chatbots.sh`) are not rows here: they are runs of `testdata/chatbots/`.

| Sender | spoor shows | Missing |
|---|---|---|
| [Claude Code](claude-code.md) | tree, model, all token buckets, cost, sessions, tool output | messages on model calls (not exported) |
| [OpenInference](openinference.md): OpenAI | model, tokens with cache read and reasoning, cost, messages, tool calls, session, errors | the model on a failed call |
| [OpenInference](openinference.md): Anthropic | the same, with cache write | |
| [OpenInference](openinference.md): LangChain | the same, plus the graph's chain and tool spans | |
| [OpenInference](openinference.md): LlamaIndex | the same, plus the workflow's chain and tool spans | |
| [OpenInference](openinference.md): OpenAI Agents SDK | agent tree, model, tokens, cost, messages, tool spans, session | cache read and reasoning tokens (not sent) |
| [OpenInference](openinference.md): CrewAI, with the OpenAI instrumentor | the crew's chain and agent spans, model, tokens with cache read, cost, messages, session | |
| [OpenLLMetry](openllmetry.md): OpenAI | model, tokens with cache read and reasoning, cost, messages, tool calls, session, errors | |
| [OpenLLMetry](openllmetry.md): Anthropic | the same, with cache write | |
| [OpenTelemetry's OpenAI instrumentation](opentelemetry-openai.md) | model, tokens, cost, errors; messages and tool calls in `span_only` mode | cache read and reasoning tokens (not sent); content sent as log records |
| [Vercel AI SDK](vercel-ai-sdk.md) | agent tree, model, tokens with cache read, cost, messages, tool spans | reasoning tokens, session (not sent) |
| [Pydantic AI](pydantic-ai.md) | agent tree, model, tokens with cache read and reasoning, cost, messages, tool spans, session | |
| [LiteLLM](litellm.md) | model, tokens, LiteLLM's own cost, messages, tool call | cache read and reasoning tokens (not sent) |
| [Gemini CLI](gemini-cli.md) | model, tokens, cost, messages, session | cached and thinking tokens (not sent) |
| [GitHub Copilot Chat in VS Code](copilot-chat.md) | agent tree per turn, model, tokens with cache read and reasoning, cost, messages, tool spans, session, errors | the whole prompt on each call (only what was added is sent); reasoning text (encrypted) |

## Cache counts

The context chart splits each prompt into cache read, fresh input and cache write, and the cache-loss marks compare one call's split with the next. Both need the sender to report how much of the prompt was read from the provider's cache and how much was written to it. Without those counts the chart shows the prompt as one block, no cache loss is marked, and a cost spoor calculates prices the whole prompt as fresh input and is marked `~`.

What the model calls of each capture carry, as spoor stores them:

| Sender | Cache read | Cache write |
|---|---|---|
| Claude Code | yes | yes |
| OpenInference: Anthropic | yes | yes |
| OpenInference: LangChain | yes | yes with a Claude model (`chatbot-langchain`); none with the OpenAI model of `openinference-langchain-tool` |
| OpenInference: OpenAI, CrewAI | yes | none: the OpenAI API reports no cache write |
| OpenInference: LlamaIndex | on 2 of the 4 model-call spans of the capture | none, as above |
| OpenInference: OpenAI Agents SDK | no | no |
| OpenLLMetry: Anthropic | yes | yes |
| OpenLLMetry: OpenAI | yes | none, as above |
| OpenTelemetry's OpenAI instrumentation | no | no |
| Vercel AI SDK | yes | not in the capture (an OpenAI model) |
| Pydantic AI | yes | not in the capture (an OpenAI model) |
| LiteLLM | no: only as text inside `metadata.usage_object` | no |
| Gemini CLI | no | no |
| GitHub Copilot Chat in VS Code | yes, on stored rows of a real session | none, as above (an OpenAI model) |

A "no" is what that version of the sender put on the span, not a limit of the provider.

Codex CLI is not here because its documentation describes log export only, and spoor stores traces.

Any other sender can go [behind an OpenTelemetry Collector](otel-collector.md).
