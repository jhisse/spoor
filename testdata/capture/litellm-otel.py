# /// script
# requires-python = ">=3.12"
# dependencies = ["litellm==1.104.0", "opentelemetry-sdk==1.45.0", "opentelemetry-exporter-otlp-proto-http==1.45.0"]
# [tool.uv]
# exclude-newer = "2026-10-05T03:23:43Z"
# ///
import os
import sys

import litellm

SCENARIO = sys.argv[1] if len(sys.argv) > 1 else "plain"
QUESTION = "Weather in Paris?" if SCENARIO == "tool" else "Capital of France?"

os.environ["OTEL_EXPORTER"] = "otlp_http"
os.environ["OTEL_ENDPOINT"] = os.environ["OTEL_EXPORTER_OTLP_ENDPOINT"] + "/v1/traces"
litellm.callbacks = ["otel"]
tools = [{"type": "function", "function": {"name": "get_weather", "description": "Weather in a city", "parameters": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}}}]
litellm.completion(
    model="openai/gpt-4o-mini",
    api_base=os.environ["OPENAI_BASE_URL"],
    messages=[{"role": "system", "content": "Answer in one word."}, {"role": "user", "content": QUESTION}],
    tools=None if SCENARIO == "plain" else tools,
)
