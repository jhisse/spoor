# /// script
# requires-python = ">=3.12"
# dependencies = ["openinference-instrumentation-anthropic==3.0.1", "anthropic==1.11.0", "opentelemetry-sdk==1.45.0", "opentelemetry-exporter-otlp-proto-http==1.45.0"]
# [tool.uv]
# exclude-newer = "2026-10-05T03:23:43Z"
# ///
import sys

from anthropic import Anthropic
from openinference.instrumentation import using_session
from openinference.instrumentation.anthropic import AnthropicInstrumentor
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor

SCENARIO = sys.argv[1] if len(sys.argv) > 1 else "plain"
QUESTION = "Weather in Paris?" if SCENARIO == "tool" else "Capital of France?"
MODEL = "no-such-model" if SCENARIO == "error" else "claude-haiku-4-5"  # the fake answers 404; the SDK raises, the span is exported
if SCENARIO == "error":
    sys.excepthook = lambda *exc: None  # no traceback on the terminal

provider = TracerProvider()
provider.add_span_processor(SimpleSpanProcessor(OTLPSpanExporter()))
trace.set_tracer_provider(provider)
AnthropicInstrumentor().instrument(tracer_provider=provider)
with using_session("capture-session"):
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
