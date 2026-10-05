# Chatbots for measuring spoor with real models

Nine small chat bots, one per folder, each a single file that runs with `uv` (no install, no virtualenv: the
dependencies are in the file's header, pinned, with `exclude-newer` for the transitive ones). Each uses a different framework, a different way of producing
OpenTelemetry traces and a different kind of key, and each loads what its framework offers beyond plain
tool calls. They exist to send spoor what real SDKs send, not canned captures (`../capture` is for those).

| Folder | Framework | Traces come from | Key |
|---|---|---|---|
| `langchain-claude` | LangChain Deep Agents | OpenInference (`openinference-instrumentation-langchain`) | `ANTHROPIC_API_KEY` |
| `llamaindex-compatible` | LlamaIndex agent workflow | OpenLLMetry (`traceloop-sdk`) | `OPENAI_API_KEY` + `OPENAI_BASE_URL` |
| `pydanticai-gemini` | Pydantic AI | the framework's own OpenTelemetry | `GEMINI_API_KEY`, or `OPENAI_API_KEY` |
| `openai-agents` | OpenAI Agents SDK | OpenInference (`...-openai-agents`) | `OPENAI_API_KEY` + `OPENAI_BASE_URL` |
| `claude-agent-sdk` | Claude Agent SDK | the telemetry Claude Code itself exports | `ANTHROPIC_API_KEY` |
| `litellm-multiprovider` | LiteLLM Router | LiteLLM's `otel` callback | any of Anthropic, OpenAI, Gemini, Groq, Mistral, DeepSeek, OpenRouter |
| `smolagents-hybrid` | Hugging Face smolagents | OpenInference (`...-smolagents`) | `ANTHROPIC_API_KEY`, or `OPENAI_API_KEY` |
| `plain-anthropic` | none (Anthropic SDK) | OpenLLMetry's instrumentation + hand-written spans | `ANTHROPIC_API_KEY` |
| `plain-openai` | none (OpenAI SDK, streaming) | the official OpenTelemetry instrumentation + hand-written spans | `OPENAI_API_KEY` + `OPENAI_BASE_URL` |

What each loads: function tools (`city_info`, `get_time`, `calculate`), an MCP server started with
`uvx mcp-server-time`, and a skill (`unit-converter`). Where the framework has a native mechanism it is used:
`SKILL.md` and subagents in Deep Agents and the Claude Agent SDK, a retriever tool and a handoff in LlamaIndex,
a toolset and typed dependencies in Pydantic AI, handoffs, an agent as a tool, a guardrail and a session in the
OpenAI Agents SDK, a Router with fallbacks in LiteLLM, a managed sub-agent in smolagents. Elsewhere a `load_skill`
tool does the same job.

## Run

```sh
cp .env.example .env          # fill in the keys you have
ENV_FILE=.env ./run-all.sh    # one turn in every bot whose key is set
cd langchain-claude
uv run --env-file ../.env bot.py                       # chat: /new starts a session, /quit leaves
uv run --env-file ../.env bot.py --once "How many feet in 3 km?"
./bot.py --once "..."         # the same, if the variables are exported (the file has a uv shebang)
```

One OpenRouter key runs all nine: `OPENAI_BASE_URL=https://openrouter.ai/api/v1` and `MODEL=openai/gpt-4o-mini` for the
OpenAI-compatible bots, `ANTHROPIC_BASE_URL=https://openrouter.ai/api` and `MODEL=anthropic/claude-haiku-4.5` for the
Anthropic ones, `OPENROUTER_API_KEY` for LiteLLM. About nine cents for three turns in each.

For a reasoning model set `MODEL` (`openai/gpt-5-mini`, `google/gemini-2.5-flash`); `plain-anthropic` also takes
`THINKING_TOKENS=2000` for Claude's extended thinking.

Point them at spoor with `OTEL_EXPORTER_OTLP_ENDPOINT` (default `http://127.0.0.1:4318`, spoor's ingest port).
`--no-mcp` skips the MCP server. `uvx` runs published tools, so it starts the MCP server here; a local script
is run with `uv run`.

Everything the bots say and every tool they call ends up in the traces: use a throwaway database.

## Without a key

The fake provider in `../capture` answers OpenAI, Anthropic and Gemini calls with canned replies:

```sh
(cd ../capture && uv run fake.py) &        # port 14331
ANTHROPIC_API_KEY=x ANTHROPIC_BASE_URL=http://127.0.0.1:14331 ./bot.py --once "hi"
OPENAI_API_KEY=x OPENAI_BASE_URL=http://127.0.0.1:14331/v1 ./bot.py --once "hi"
```

`pydanticai-gemini` and `claude-agent-sdk` also have `--selftest`, which needs no provider. `claude-agent-sdk` loads its skill and tools
without a key but needs a real model for the answer, because Claude Code streams.
