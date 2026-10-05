# /// script
# requires-python = ">=3.12"
# dependencies = ["openinference-instrumentation-openai-agents==2.5.2", "openai-agents==0.23.1", "opentelemetry-sdk==1.45.0", "opentelemetry-exporter-otlp-proto-http==1.45.0"]
# [tool.uv]
# exclude-newer = "2026-10-05T03:23:43Z"
# ///
import sys

from agents import Agent, Runner, function_tool, set_default_openai_api
from openinference.instrumentation import using_session
from openinference.instrumentation.openai_agents import OpenAIAgentsInstrumentor
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor

SCENARIO = sys.argv[1] if len(sys.argv) > 1 else "plain"
QUESTION = "Weather in Paris?" if SCENARIO == "tool" else "Capital of France?"


@function_tool
def get_weather(city: str) -> str:
    """Weather in a city"""
    return "18 C, clear"


provider = TracerProvider()
provider.add_span_processor(SimpleSpanProcessor(OTLPSpanExporter()))
trace.set_tracer_provider(provider)
OpenAIAgentsInstrumentor().instrument(tracer_provider=provider)
set_default_openai_api("chat_completions")
tools = [] if SCENARIO == "plain" else [get_weather]
agent = Agent(name="assistant", instructions="Answer in one word.", model="gpt-4o-mini", tools=tools)
with using_session("capture-session"):
    Runner.run_sync(agent, QUESTION)
