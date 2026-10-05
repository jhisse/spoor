# /// script
# requires-python = ">=3.12"
# dependencies = ["traceloop-sdk==0.62.4", "openai==3.24.0", "requests==2.34.2", "httpx==0.28.1"]
# [tool.uv]
# exclude-newer = "2026-10-05T03:23:43Z"
# ///
import sys

from openai import OpenAI
from opentelemetry import trace
from traceloop.sdk import Traceloop

SCENARIO = sys.argv[1] if len(sys.argv) > 1 else "plain"
QUESTION = "Weather in Paris?" if SCENARIO == "tool" else "Capital of France?"
MODEL = "no-such-model" if SCENARIO == "error" else "gpt-4o-mini"  # the fake answers 404; the SDK raises, the span is exported
if SCENARIO == "error":
    sys.excepthook = lambda *exc: None  # no traceback on the terminal

Traceloop.init(app_name="capture", disable_batch=True)
Traceloop.set_association_properties({"session_id": "capture-session"})
client = OpenAI()
messages = [{"role": "system", "content": "Answer in one word."}, {"role": "user", "content": QUESTION}]
tools = [{"type": "function", "function": {"name": "get_weather", "description": "Weather in a city", "parameters": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}}}]
if SCENARIO != "tool":
    client.chat.completions.create(model=MODEL, messages=messages)
else:
    with trace.get_tracer("capture").start_as_current_span("weather-agent"):
        call = client.chat.completions.create(model=MODEL, messages=messages, tools=tools).choices[0].message
        messages += [call.model_dump(exclude_none=True), {"role": "tool", "tool_call_id": call.tool_calls[0].id, "content": "18 C, clear"}]
        client.chat.completions.create(model=MODEL, messages=messages, tools=tools)
