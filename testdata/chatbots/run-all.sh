#!/bin/sh
# usage: ./run-all.sh ["prompt"]    one turn in every bot whose key is set (from the environment or ../.env style file)
# Env file: ENV_FILE=.env ./run-all.sh   Needs uv. Each bot sends its traces to OTEL_EXPORTER_OTLP_ENDPOINT.
cd "$(dirname "$0")" || exit 1
[ -n "${ENV_FILE:-}" ] && set -a && . "$ENV_FILE" && set +a
prompt=${1:-"What is 17 * 23, and what time is it in Tokyo?"}
run() { # name, then the variables of which one must be set
	name=$1; shift
	for var; do
		eval "val=\${$var:-}"
		if [ -n "$val" ]; then
			printf '== %s (%s)\n' "$name" "$var"
			(cd "$name" && uv run --quiet bot.py --once "$prompt" 2>&1 | grep -v -E 'Failed to validate|validation error|Request\.|ClientRequest|^[[:space:]]+(For further|Input should|Field required)') || echo "   failed"
			return
		fi
	done
	printf '-- %s skipped (set %s)\n' "$name" "$*"
}
run langchain-claude ANTHROPIC_API_KEY
run claude-agent-sdk ANTHROPIC_API_KEY
run plain-anthropic ANTHROPIC_API_KEY
run llamaindex-compatible OPENAI_API_KEY
run openai-agents OPENAI_API_KEY
run plain-openai OPENAI_API_KEY
run pydanticai-gemini GEMINI_API_KEY OPENAI_API_KEY
run smolagents-hybrid ANTHROPIC_API_KEY OPENAI_API_KEY
run litellm-multiprovider ANTHROPIC_API_KEY OPENAI_API_KEY GEMINI_API_KEY GROQ_API_KEY MISTRAL_API_KEY DEEPSEEK_API_KEY OPENROUTER_API_KEY
