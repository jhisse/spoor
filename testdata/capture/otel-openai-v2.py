# /// script
# requires-python = ">=3.12"
# dependencies = ["opentelemetry-instrumentation-openai-v2==2.4b0", "openai==3.24.0", "httpx==0.28.1", "opentelemetry-util-genai==1.1b0", "opentelemetry-sdk==1.45.0", "opentelemetry-exporter-otlp-proto-http==1.45.0"]
# [tool.uv]
# exclude-newer = "2026-10-05T03:23:43Z"
# ///
# opentelemetry-util-genai stays at 1.1b0: 1.2b0 dropped the module that the newest
# openai-v2 (2.4b0) imports.
# Scenario "events" sends content as a log event only, which spoor discards;
# the others put it on the span.
import os
import sys

from openai import OpenAI
from opentelemetry import trace
from opentelemetry._logs import set_logger_provider
from opentelemetry.exporter.otlp.proto.http._log_exporter import OTLPLogExporter
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.instrumentation.openai_v2 import OpenAIInstrumentor
from opentelemetry.sdk._logs import LoggerProvider
from opentelemetry.sdk._logs.export import SimpleLogRecordProcessor
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor

os.environ["OTEL_SEMCONV_STABILITY_OPT_IN"] = "gen_ai_latest_experimental"
os.environ["OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT"] = "span_only"
SCENARIO = sys.argv[1] if len(sys.argv) > 1 else "plain"
QUESTION = "Weather in Paris?" if SCENARIO == "tool" else "Capital of France?"
if SCENARIO == "events":
    os.environ["OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT"] = "event_only"
    SCENARIO, QUESTION = "plain", "Capital of France?"
MODEL = "no-such-model" if SCENARIO == "error" else "gpt-4o-mini"  # the fake answers 404; the SDK raises, the span is exported
if SCENARIO == "error":
    sys.excepthook = lambda *exc: None  # no traceback on the terminal

logs = LoggerProvider()
logs.add_log_record_processor(SimpleLogRecordProcessor(OTLPLogExporter()))
set_logger_provider(logs)
provider = TracerProvider()
provider.add_span_processor(SimpleSpanProcessor(OTLPSpanExporter()))
trace.set_tracer_provider(provider)
OpenAIInstrumentor().instrument(tracer_provider=provider)
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
