#!/bin/sh
# Starts the fake provider, runs every capture script, stops the fake.
# Needs uv and node (npm install once, for vercel-ai.mjs; npx fetches Gemini CLI).
set -eu
cd "$(dirname "$0")"
rm -rf out
uv run --quiet fake.py "${PORT:-14331}" 2>/dev/null &
fake=$!
trap 'kill $fake' EXIT
sleep 3
./capture.sh openinference-openai.py plain tool error
./capture.sh openinference-anthropic.py plain tool error
./capture.sh openinference-langchain.py tool
./capture.sh openinference-crewai.py workflow
./capture.sh openinference-llamaindex.py tool
./capture.sh openinference-openai-agents.py tool
./capture.sh traceloop-openai.py plain tool error
./capture.sh traceloop-anthropic.py plain tool
./capture.sh otel-openai-v2.py tool events error
./capture.sh pydantic-ai.py tool
./capture.sh litellm-otel.py tool
./capture.sh vercel-ai.mjs tool
./gemini-cli.sh
