#!/bin/sh
# Gemini CLI against the fake, in an empty HOME: no login, no saved settings.
# usage: ./gemini-cli.sh     (needs fake.py running; writes out/gemini-cli-plain.pb)
set -eu
cd "$(dirname "$0")"
base=http://127.0.0.1:${PORT:-14331}
home=/tmp/spoor-capture-home && rm -rf "$home"
trap 'rm -rf "$home"' EXIT
mkdir -p "$home/.gemini" "$home/work"
echo '{"privacy": {"usageStatisticsEnabled": false}, "telemetry": {"logPrompts": true, "traces": true}, "security": {"auth": {"selectedType": "gemini-api-key"}}}' >"$home/.gemini/settings.json"
rm -f out/gemini-cli-plain.pb out/gemini-cli-plain.json
cd "$home/work"
HOME=$home GEMINI_API_KEY=fake GOOGLE_GEMINI_BASE_URL=$base \
	GEMINI_CLI_TRUST_WORKSPACE=true GEMINI_TELEMETRY_ENABLED=true GEMINI_TELEMETRY_OTLP_PROTOCOL=http \
	GEMINI_TELEMETRY_OTLP_ENDPOINT=$base/gemini-cli-plain \
	npx --yes @google/gemini-cli@0.62.0 --model gemini-3.6-flash --prompt "Capital of France?"
