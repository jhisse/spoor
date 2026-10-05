# /// script
# requires-python = ">=3.12"
# dependencies = ["openinference-instrumentation-openai==0.1.63", "openai==3.24.0", "opentelemetry-sdk==1.45.0", "opentelemetry-exporter-otlp-proto-http==1.45.0"]
# [tool.uv]
# exclude-newer = "2026-10-05T03:23:43Z"
# ///
import sys

from openai import OpenAI
from openinference.instrumentation import using_session
from openinference.instrumentation.openai import OpenAIInstrumentor
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor

SCENARIO = sys.argv[1] if len(sys.argv) > 1 else "plain"
QUESTION = "Weather in Paris?" if SCENARIO == "tool" else "Capital of France?"
MODEL = "no-such-model" if SCENARIO == "error" else "gpt-4o-mini"  # the fake answers 404; the SDK raises, the span is exported
if SCENARIO == "error":
    sys.excepthook = lambda *exc: None  # no traceback on the terminal

provider = TracerProvider()
provider.add_span_processor(SimpleSpanProcessor(OTLPSpanExporter()))
trace.set_tracer_provider(provider)
OpenAIInstrumentor().instrument(tracer_provider=provider)
with using_session("capture-session"):
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
