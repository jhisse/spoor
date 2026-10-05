#!/bin/sh
# usage: OPENROUTER_API_KEY=... ./chatbots.sh     (spends a few cents of real model calls)
# Makes eleven runs of seven of the ../chatbots with real models through OpenRouter and records their traces with the
# capture sink: out/chatbot-<name>.pb and .json. No MCP server (its startup spans are traces of their
# own) and not claude-agent-sdk (it exports Claude Code's spans, which claude-code-* already cover, and the
# session-title call as a second trace).
set -eu
cd "$(dirname "$0")"
: "${OPENROUTER_API_KEY:?set OPENROUTER_API_KEY}"
rm -rf out
uv run --quiet fake.py "${PORT:-14331}" 2>/dev/null &
sink=$!
trap 'kill $sink' EXIT
sleep 3
base=http://127.0.0.1:${PORT:-14331}
prompt="What is 17 * 23, and what time is it in Tokyo?"
run() { # capture name, bot folder, then the variables of that run
	name=$1 bot=$2
	shift 2
	echo "== $name"
	(cd "../chatbots/$bot" && env -u ANTHROPIC_API_KEY -u GEMINI_API_KEY "$@" OTEL_EXPORTER_OTLP_ENDPOINT="$base/$name" \
		TRACELOOP_BASE_URL="$base/$name" TRACELOOP_TELEMETRY=false uv run --quiet bot.py --once "$prompt" --no-mcp | tail -1)
}
oa="OPENAI_API_KEY=$OPENROUTER_API_KEY OPENAI_BASE_URL=https://openrouter.ai/api/v1"
an="ANTHROPIC_API_KEY=$OPENROUTER_API_KEY ANTHROPIC_BASE_URL=https://openrouter.ai/api"
# shellcheck disable=SC2086 # the variables are words on purpose
{
	run chatbot-plain-openai plain-openai $oa MODEL=openai/gpt-4o-mini
	run chatbot-plain-openai-reasoning plain-openai $oa MODEL=openai/gpt-5-mini
	run chatbot-plain-anthropic plain-anthropic $an MODEL=anthropic/claude-haiku-4.5
	run chatbot-plain-anthropic-thinking plain-anthropic $an MODEL=anthropic/claude-haiku-4.5 THINKING_TOKENS=2000
	run chatbot-openai-agents openai-agents $oa MODEL=openai/gpt-4o-mini
	run chatbot-llamaindex llamaindex-compatible $oa MODEL=openai/gpt-4o-mini
	run chatbot-llamaindex-reasoning llamaindex-compatible $oa MODEL=openai/gpt-5-mini
	run chatbot-pydanticai pydanticai-gemini $oa MODEL=openai/gpt-4o-mini
	run chatbot-pydanticai-reasoning pydanticai-gemini $oa MODEL=openai/gpt-5-mini
	run chatbot-smolagents smolagents-hybrid $oa MODEL=openai/gpt-4o-mini
	run chatbot-langchain langchain-claude $an MODEL=anthropic/claude-haiku-4.5
}
