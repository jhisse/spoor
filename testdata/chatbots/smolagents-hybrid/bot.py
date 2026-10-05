#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["smolagents[openai,litellm,mcp]==1.26.0", "mcp==1.30.0", "openinference-instrumentation-smolagents==0.1.42", "opentelemetry-sdk==1.45.0", "opentelemetry-exporter-otlp-proto-http==1.45.0"]
# [tool.uv]
# exclude-newer = "2026-10-05T03:23:43Z"
# ///
"""Hugging Face smolagents on Claude (through LiteLLM) or any OpenAI-compatible endpoint, traced by OpenInference.

Loads what the framework offers: @tool functions, an MCP server (uvx mcp-server-time) through ToolCollection,
a skill loaded on demand and a managed sub-agent.

    ANTHROPIC_API_KEY=... ./bot.py                                        # Claude
    OPENAI_API_KEY=... [OPENAI_BASE_URL=...] ./bot.py --once "What is 12 * 12?"   # OpenAI-compatible

Environment: ANTHROPIC_API_KEY or OPENAI_API_KEY, MODEL (default claude-haiku-4-5 / gpt-4o-mini), OPENAI_BASE_URL,
OTEL_EXPORTER_OTLP_ENDPOINT (default http://127.0.0.1:4318), OTEL_SERVICE_NAME. --no-mcp skips the MCP server.
"""
import ast
import contextlib
import operator
import os
import sys
import uuid
from datetime import datetime
from zoneinfo import ZoneInfo

from mcp import StdioServerParameters
from openinference.instrumentation import using_session
from openinference.instrumentation.smolagents import SmolagentsInstrumentor
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from smolagents import LiteLLMModel, OpenAIServerModel, ToolCallingAgent, ToolCollection, tool

SKILLS = {
    "unit-converter": "Convert metric and imperial units with the calculate tool. 1 km = 0.621371 mi, 1 m = 3.28084 ft, "
                      "1 kg = 2.20462 lb, C to F = C * 9 / 5 + 32. Answer rounded to two decimals, naming the factor.",
}
CITIES = {"paris": "France, timezone Europe/Paris, about 2.1 million people", "tokyo": "Japan, timezone Asia/Tokyo, about 14 million people"}
OPS = {ast.Add: operator.add, ast.Sub: operator.sub, ast.Mult: operator.mul, ast.Div: operator.truediv, ast.Pow: operator.pow}


def evaluate(node):
    if isinstance(node, ast.Expression):
        return evaluate(node.body)
    if isinstance(node, ast.Constant) and isinstance(node.value, (int, float)):
        return node.value
    if isinstance(node, ast.BinOp) and type(node.op) in OPS:
        return OPS[type(node.op)](evaluate(node.left), evaluate(node.right))
    raise ValueError("only + - * / ** on numbers")


@tool
def city_info(city: str) -> str:
    """A short fact sheet about a city.

    Args:
        city: The city name.
    """
    return CITIES.get(city.lower(), f"no sheet for {city}")


@tool
def get_time(timezone: str = "UTC") -> str:
    """Current date and time in an IANA timezone, such as Asia/Tokyo.

    Args:
        timezone: An IANA timezone name.
    """
    try:
        return datetime.now(ZoneInfo(timezone)).isoformat(timespec="seconds")
    except Exception:
        return f"error: unknown timezone {timezone}"


@tool
def calculate(expression: str) -> str:
    """Evaluate an arithmetic expression such as (3 + 4) * 2.

    Args:
        expression: The expression.
    """
    try:
        return str(evaluate(ast.parse(expression, mode="eval")))
    except (ValueError, SyntaxError, ZeroDivisionError, OverflowError) as err:
        return f"error: {err}"


@tool
def load_skill(name: str) -> str:
    """Load the instructions of a skill by name. Skills: unit-converter.

    Args:
        name: The skill name.
    """
    return SKILLS.get(name, f"no skill {name}; skills: {', '.join(SKILLS)}")


def model():
    if os.environ.get("ANTHROPIC_API_KEY"):
        return LiteLLMModel(model_id="anthropic/" + os.environ.get("MODEL", "claude-haiku-4-5"), api_key=os.environ["ANTHROPIC_API_KEY"])
    if os.environ.get("OPENAI_API_KEY"):
        return OpenAIServerModel(model_id=os.environ.get("MODEL", "gpt-4o-mini"), api_base=os.environ.get("OPENAI_BASE_URL"), api_key=os.environ["OPENAI_API_KEY"])
    sys.exit("set ANTHROPIC_API_KEY, or OPENAI_API_KEY (and OPENAI_BASE_URL for a compatible endpoint)")


def main():
    llm = model()
    endpoint = os.environ.get("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318").rstrip("/")
    provider = TracerProvider(resource=Resource.create({"service.name": os.environ.get("OTEL_SERVICE_NAME", "bot-smolagents-hybrid")}))
    provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter(endpoint=endpoint + "/v1/traces")))
    SmolagentsInstrumentor().instrument(tracer_provider=provider)

    with contextlib.ExitStack() as stack:
        tools = [city_info, get_time, calculate, load_skill]
        if "--no-mcp" not in sys.argv:
            tools += stack.enter_context(ToolCollection.from_mcp(StdioServerParameters(command="uvx", args=["mcp-server-time==2026.8.18"]), trust_remote_code=True)).tools
        researcher = ToolCallingAgent(tools=[city_info], model=llm, name="researcher", description="Looks up facts about cities.", max_steps=3)
        agent = ToolCallingAgent(tools=tools, model=llm, managed_agents=[researcher], max_steps=6, instructions=(
            "You are a concise assistant. Use a tool when it helps, then answer in one or two sentences. Skills you can load: " + ", ".join(SKILLS) + "."))
        session = str(uuid.uuid4())

        def turn(text):
            with using_session(session):
                reply = agent.run(text, reset=False)  # keep the memory of earlier turns
            provider.force_flush()
            return str(reply)

        if "--once" in sys.argv:
            print(turn(sys.argv[sys.argv.index("--once") + 1]))
            return
        print(f"smolagents, session {session[:8]}, {len(tools)} tools, 1 managed agent. /new, /quit.")
        while True:
            try:
                text = input("you> ").strip()
            except EOFError:
                break
            if not sys.stdin.isatty():  # a piped session: show what was typed
                print(text)
            if text == "/quit":
                break
            if text == "/new":
                session = str(uuid.uuid4())
                agent.memory.reset()
                print(f"session {session[:8]}")
            elif text:
                print("bot>", turn(text))


if __name__ == "__main__":
    main()
