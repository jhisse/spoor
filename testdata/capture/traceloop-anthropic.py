# /// script
# requires-python = ">=3.12"
# dependencies = ["traceloop-sdk==0.62.4", "anthropic==1.11.0", "requests==2.34.2", "httpx==0.28.1"]
# [tool.uv]
# exclude-newer = "2026-10-05T03:23:43Z"
# ///
import sys

from anthropic import Anthropic
from opentelemetry import trace
from traceloop.sdk import Traceloop

SCENARIO = sys.argv[1] if len(sys.argv) > 1 else "plain"
QUESTION = "Weather in Paris?" if SCENARIO == "tool" else "Capital of France?"
MODEL = "claude-haiku-4-5"
Traceloop.init(app_name="capture", disable_batch=True)
Traceloop.set_association_properties({"session_id": "capture-session"})
client = Anthropic()
messages = [{"role": "user", "content": QUESTION}]
args = {"model": MODEL, "max_tokens": 64, "system": "Answer in one word."}
if SCENARIO != "tool":
    client.messages.create(messages=messages, **args)
else:
    args["tools"] = [{"name": "get_weather", "description": "Weather in a city", "input_schema": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}}]
    with trace.get_tracer("capture").start_as_current_span("weather-agent"):
        call = client.messages.create(messages=messages, **args)
        result = {"type": "tool_result", "tool_use_id": call.content[0].id, "content": "18 C, clear"}
        messages += [{"role": "assistant", "content": [call.content[0].model_dump()]}, {"role": "user", "content": [result]}]
        client.messages.create(messages=messages, **args)
