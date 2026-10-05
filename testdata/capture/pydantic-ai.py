# /// script
# requires-python = ">=3.12"
# dependencies = ["pydantic-ai-slim[openai]==2.54.0", "opentelemetry-sdk==1.45.0", "opentelemetry-exporter-otlp-proto-http==1.45.0"]
# [tool.uv]
# exclude-newer = "2026-10-05T03:23:43Z"
# ///
import sys

from pydantic_ai import Agent
from pydantic_ai.models.openai import OpenAIChatModel
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor

SCENARIO = sys.argv[1] if len(sys.argv) > 1 else "plain"
QUESTION = "Weather in Paris?" if SCENARIO == "tool" else "Capital of France?"

provider = TracerProvider()
provider.add_span_processor(SimpleSpanProcessor(OTLPSpanExporter()))
trace.set_tracer_provider(provider)
Agent.instrument_all()
agent = Agent(OpenAIChatModel("gpt-4o-mini"), system_prompt="Answer in one word.")
if SCENARIO == "tool":

    @agent.tool_plain
    def get_weather(city: str) -> str:
        """Weather in a city"""
        return "18 C, clear"


agent.run_sync(QUESTION)
