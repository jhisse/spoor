# /// script
# requires-python = ">=3.12"
# dependencies = ["openinference-instrumentation-llama-index==4.5.4", "llama-index-core==0.14.25", "llama-index-llms-openai==0.8.2", "opentelemetry-sdk==1.45.0", "opentelemetry-exporter-otlp-proto-http==1.45.0"]
# [tool.uv]
# exclude-newer = "2026-10-05T03:23:43Z"
# ///
import asyncio
import os
import sys

from llama_index.core.agent.workflow import FunctionAgent
from llama_index.llms.openai import OpenAI
from openinference.instrumentation import using_session
from openinference.instrumentation.llama_index import LlamaIndexInstrumentor
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor

SCENARIO = sys.argv[1] if len(sys.argv) > 1 else "plain"
QUESTION = "Weather in Paris?" if SCENARIO == "tool" else "Capital of France?"


def get_weather(city: str) -> str:
    """Weather in a city"""
    return "18 C, clear"


async def main():
    tools = [] if SCENARIO == "plain" else [get_weather]
    agent = FunctionAgent(llm=OpenAI(model="gpt-4o-mini", api_base=os.environ["OPENAI_BASE_URL"]), tools=tools, system_prompt="Answer in one word.", streaming=False)
    with using_session("capture-session"):
        await agent.run(QUESTION)


provider = TracerProvider()
provider.add_span_processor(SimpleSpanProcessor(OTLPSpanExporter()))
trace.set_tracer_provider(provider)
LlamaIndexInstrumentor().instrument(tracer_provider=provider)
asyncio.run(main())
