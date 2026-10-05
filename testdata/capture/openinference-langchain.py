# /// script
# requires-python = ">=3.12"
# dependencies = ["openinference-instrumentation-langchain==0.1.78", "langchain==1.4.3", "langchain-openai==1.6.7", "opentelemetry-sdk==1.45.0", "opentelemetry-exporter-otlp-proto-http==1.45.0"]
# [tool.uv]
# exclude-newer = "2026-10-05T03:23:43Z"
# ///
import sys

from langchain.agents import create_agent
from langchain_core.tools import tool
from langchain_openai import ChatOpenAI
from openinference.instrumentation import using_session
from openinference.instrumentation.langchain import LangChainInstrumentor
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor

SCENARIO = sys.argv[1] if len(sys.argv) > 1 else "plain"
QUESTION = "Weather in Paris?" if SCENARIO == "tool" else "Capital of France?"


@tool
def get_weather(city: str) -> str:
    """Weather in a city"""
    return "18 C, clear"


provider = TracerProvider()
provider.add_span_processor(SimpleSpanProcessor(OTLPSpanExporter()))
trace.set_tracer_provider(provider)
LangChainInstrumentor().instrument(tracer_provider=provider)
tools = [] if SCENARIO == "plain" else [get_weather]
agent = create_agent(ChatOpenAI(model="gpt-4o-mini"), tools, system_prompt="Answer in one word.")
with using_session("capture-session"):
    agent.invoke({"messages": [("user", QUESTION)]})
