# Claude Code

## What you get

- **One trace per prompt**, named `claude_code.interaction`. Under it, as siblings, one `claude_code.llm_request` per model call and one `claude_code.tool <Tool>` per tool call; under each tool, its waiting (`blocked_on_user`) and `execution` phases.
- **Model and all four token buckets** on every model call. Claude Code reports input without the cached part; spoor adds it back, so "input" is the whole prompt as everywhere else.
- **Cost, calculated by spoor** per bucket from its price table. Claude Code puts no cost on spans. It is an API-price figure: on a subscription plan it is not what you pay.
- **Sessions.** Every span carries `session.id`, so the prompts of one Claude Code session group on the sessions page.
- **Background model calls** (titles, permission checks) are there and counted, because they were made.

## Configuration

```sh
export CLAUDE_CODE_ENABLE_TELEMETRY=1
export CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1
export OTEL_TRACES_EXPORTER=otlp
export OTEL_METRICS_EXPORTER=none
export OTEL_LOGS_EXPORTER=none
export OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=http/protobuf
export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://127.0.0.1:4318/v1/traces
```

Then run `claude` as usual. No key or header is needed. Use `http/protobuf` or `http/json`, not `grpc`.

Optional. Each adds content to the spans, readable by anyone who can open spoor:

- `OTEL_LOG_USER_PROMPTS=1`: the text you typed, shown as the input of the interaction span.
- `OTEL_LOG_TOOL_DETAILS=1`: file paths and commands, in the tool spans' metadata.
- `OTEL_LOG_TOOL_CONTENT=1`: tool output, as a span event. spoor shows it as the tool span's input and output.

## Tested

2026-10-03, Claude Code 2.1.287. Two exports of `claude -p` are in `testdata/` (`claude-code-simple`, `claude-code-tool-read`); translation and page tests run against them. Two more fixtures (`claude-code-interactive-tools`, `claude-code-interactive-parallel`) are interactive turns rebuilt from stored rows with every free-text value replaced; they are not wire captures.

Documentation read on the same day: <https://code.claude.com/docs/en/monitoring-usage>. Trace export is a beta feature there, and attribute names may change.

## Known gaps

- **No prompt or response text on model calls.** Claude Code does not export messages on `llm_request` spans. The context chart works from token counts; the "what is new in this prompt" view says it has no basis.
- **The root span arrives last.** While a turn runs, its trace has no root: the list marks it and shows a child span's name until the root arrives.
- **Tool output is not in the `output` column.** It stays in the `tool.output` event, so search and `spoor export` see it under `events`.
- **Cache writes are priced at the 5-minute rate.** The trace does not say which lifetime was used; the span's cost breakdown shows the range.
- **Not captured:** subagents, hooks, failed calls, a session of several prompts. Per the documentation, a failed call sets status `ERROR` and a subagent's spans nest under the `Agent` tool span.
