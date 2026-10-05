#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["pydantic-ai-slim[openai,google,mcp]==2.54.0", "opentelemetry-sdk==1.45.0", "opentelemetry-exporter-otlp-proto-http==1.45.0"]
# [tool.uv]
# exclude-newer = "2026-10-05T03:23:43Z"
# ///
"""Pydantic AI agent on Gemini (or any OpenAI-compatible endpoint), with the framework's own OpenTelemetry.

Loads what the framework offers: a FunctionToolset, an MCP toolset (uvx mcp-server-time), a skill
loaded on demand, typed dependencies and dynamic instructions.

    GEMINI_API_KEY=... ./bot.py                          # Gemini
    OPENAI_API_KEY=... [OPENAI_BASE_URL=...] ./bot.py    # OpenAI-compatible
    GEMINI_API_KEY=... ./bot.py --once "What is 2 ** 10 and the time in Lisbon?"
    ./bot.py --selftest                                  # no key: calls the local tools once, lists the MCP tools

Environment: GEMINI_API_KEY or OPENAI_API_KEY, MODEL (default gemini-2.5-flash / gpt-4o-mini), OPENAI_BASE_URL,
GOOGLE_GEMINI_BASE_URL, OTEL_EXPORTER_OTLP_ENDPOINT (default http://127.0.0.1:4318), OTEL_SERVICE_NAME. --no-mcp skips MCP.
"""
import ast
import asyncio
import operator
import os
import sys
import uuid
from dataclasses import dataclass
from datetime import datetime
from zoneinfo import ZoneInfo

from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from pydantic_ai import Agent, RunContext
from pydantic_ai.agent import InstrumentationSettings
from pydantic_ai.mcp import MCPToolset
from pydantic_ai.toolsets import FunctionToolset

SKILLS = {
    "unit-converter": "Convert metric and imperial units with the calculate tool. 1 km = 0.621371 mi, 1 m = 3.28084 ft, "
                      "1 kg = 2.20462 lb, C to F = C * 9 / 5 + 32. Answer rounded to two decimals, naming the factor.",
}
CITIES = {"paris": "France, timezone Europe/Paris, about 2.1 million people", "tokyo": "Japan, timezone Asia/Tokyo, about 14 million people"}
OPS = {ast.Add: operator.add, ast.Sub: operator.sub, ast.Mult: operator.mul, ast.Div: operator.truediv, ast.Pow: operator.pow}


@dataclass
class Deps:
    user: str


def evaluate(node):
    if isinstance(node, ast.Expression):
        return evaluate(node.body)
    if isinstance(node, ast.Constant) and isinstance(node.value, (int, float)):
        return node.value
    if isinstance(node, ast.BinOp) and type(node.op) in OPS:
        return OPS[type(node.op)](evaluate(node.left), evaluate(node.right))
    raise ValueError("only + - * / ** on numbers")


tools = FunctionToolset()


@tools.tool_plain
def city_info(city: str) -> str:
    """A short fact sheet about a city."""
    return CITIES.get(city.lower(), f"no sheet for {city}")


@tools.tool_plain
def get_time(timezone: str = "UTC") -> str:
    """Current date and time in an IANA timezone, such as Asia/Tokyo."""
    try:
        return datetime.now(ZoneInfo(timezone)).isoformat(timespec="seconds")
    except Exception:
        return f"error: unknown timezone {timezone}"


@tools.tool_plain
def calculate(expression: str) -> str:
    """Evaluate an arithmetic expression such as (3 + 4) * 2."""
    try:
        return str(evaluate(ast.parse(expression, mode="eval")))
    except (ValueError, SyntaxError, ZeroDivisionError, OverflowError) as err:
        return f"error: {err}"  # the model reads it and tries again


@tools.tool_plain
def load_skill(name: str) -> str:
    """Load the instructions of a skill by name. Skills: unit-converter."""
    return SKILLS.get(name, f"no skill {name}; skills: {', '.join(SKILLS)}")


def model():
    if "--selftest" in sys.argv:  # no provider: a stand-in model that calls the local tools once
        from pydantic_ai.models.test import TestModel

        return TestModel(call_tools=["city_info", "get_time", "calculate", "load_skill"])
    if key := os.environ.get("GEMINI_API_KEY") or os.environ.get("GOOGLE_API_KEY"):
        from pydantic_ai.models.google import GoogleModel
        from pydantic_ai.providers.google import GoogleProvider

        return GoogleModel(os.environ.get("MODEL", "gemini-2.5-flash"), provider=GoogleProvider(api_key=key))
    if key := os.environ.get("OPENAI_API_KEY"):
        from pydantic_ai.models.openai import OpenAIChatModel
        from pydantic_ai.providers.openai import OpenAIProvider

        return OpenAIChatModel(os.environ.get("MODEL", "gpt-4o-mini"), provider=OpenAIProvider(base_url=os.environ.get("OPENAI_BASE_URL"), api_key=key))
    sys.exit("set GEMINI_API_KEY, or OPENAI_API_KEY (and OPENAI_BASE_URL for a compatible endpoint)")


async def main():
    llm = model()
    endpoint = os.environ.get("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318").rstrip("/")
    provider = TracerProvider(resource=Resource.create({"service.name": os.environ.get("OTEL_SERVICE_NAME", "bot-pydanticai-gemini")}))
    provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter(endpoint=endpoint + "/v1/traces")))
    Agent.instrument_all(InstrumentationSettings(tracer_provider=provider))

    toolsets, mcp = [tools], None
    if "--no-mcp" not in sys.argv:
        mcp = MCPToolset({"mcpServers": {"time": {"command": "uvx", "args": ["mcp-server-time==2026.8.18"]}}})
        toolsets.append(mcp)
    agent = Agent(llm, deps_type=Deps, toolsets=toolsets, name="pydanticai-bot",
                  instructions="You are a concise assistant. Use a tool when it helps, then answer in one or two sentences. "
                               "Skills you can load: " + ", ".join(SKILLS) + ".")

    @agent.instructions
    def user_line(ctx: RunContext[Deps]) -> str:
        return f"The user is {ctx.deps.user}."

    session, history = str(uuid.uuid4()), []

    async def turn(text):
        result = await agent.run(text, message_history=history, deps=Deps(user="a tester"), conversation_id=session)
        history[:] = result.all_messages()
        provider.force_flush()
        return result.output

    async with agent:
        if "--selftest" in sys.argv:
            await turn("self test")
            names = [p.tool_name for m in history for p in m.parts if p.part_kind == "tool-call"]
            print("tools called:", ", ".join(names))
            if mcp:
                print("mcp tools:", ", ".join(t.name for t in await mcp.list_tools()))
        elif "--once" in sys.argv:
            print(await turn(sys.argv[sys.argv.index("--once") + 1]))
        else:
            print(f"pydantic ai, session {session[:8]}. /new, /quit.")
            while True:
                try:
                    text = (await asyncio.to_thread(input, "you> ")).strip()
                except EOFError:
                    break
                if not sys.stdin.isatty():  # a piped session: show what was typed
                    print(text)
                if text == "/quit":
                    break
                if text == "/new":
                    session, history = str(uuid.uuid4()), []
                    print(f"session {session[:8]}")
                elif text:
                    print("bot>", await turn(text))
    provider.shutdown()


if __name__ == "__main__":
    asyncio.run(main())
