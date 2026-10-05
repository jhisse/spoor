#!/bin/sh
# usage: ./capture.sh <script> [scenario...]     (default scenario: plain)
# Needs fake.py running. Writes out/<script>-<scenario>.pb and .json.
set -eu
cd "$(dirname "$0")"
base=http://127.0.0.1:${PORT:-14331}
script=$1
shift
[ $# -gt 0 ] || set -- plain
case $script in *.mjs) run="node" ;; *) run="uv run --quiet" ;; esac
for scenario; do
	name=$(basename "${script%.*}")-$scenario
	rm -f "out/$name.pb" "out/$name.json"
	env OPENAI_BASE_URL=$base/v1 OPENAI_API_BASE=$base/v1 OPENAI_API_KEY=fake ANTHROPIC_BASE_URL=$base ANTHROPIC_API_KEY=fake \
		OTEL_EXPORTER_OTLP_ENDPOINT=$base/$name OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf \
		OTEL_SERVICE_NAME=capture TRACELOOP_BASE_URL=$base/$name TRACELOOP_TELEMETRY=false \
		$run "$script" "$scenario" || [ "$scenario" = error ]
	echo "out/$name.pb"
done
