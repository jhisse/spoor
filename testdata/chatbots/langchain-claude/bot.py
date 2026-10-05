#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["deepagents==0.7.21", "langchain-mcp-adapters==0.3.2", "langchain-anthropic==1.7.5", "openinference-instrumentation-langchain==0.1.78", "opentelemetry-sdk==1.45.0", "opentelemetry-exporter-otlp-proto-http==1.45.0"]
# [tool.uv]
# exclude-newer = "2026-10-05T03:23:43Z"
# ///
"""LangChain Deep Agents on Claude, traced by OpenInference.

Loads what the framework offers: function tools, an MCP server (uvx mcp-server-time),
a skill (SKILL.md), a memory file (AGENTS.md), a subagent, and the planning and
filesystem tools of Deep Agents.

    ANTHROPIC_API_KEY=... ./bot.py                       # chat
    ANTHROPIC_API_KEY=... ./bot.py --once "How many feet in 3 km?"

Environment: ANTHROPIC_API_KEY, MODEL (default claude-haiku-4-5), ANTHROPIC_BASE_URL (optional),
OTEL_EXPORTER_OTLP_ENDPOINT (default http://127.0.0.1:4318), OTEL_SERVICE_NAME. --no-mcp skips the MCP server.
"""
import ast
import asyncio
import operator
import os
import sys
import tempfile
import uuid
from datetime import datetime
from pathlib import Path
from zoneinfo import ZoneInfo

from deepagents import create_deep_agent
from deepagents.backends import FilesystemBackend
from langchain_anthropic import ChatAnthropic
from langchain_core.tools import tool
from langchain_mcp_adapters.client import MultiServerMCPClient
from openinference.instrumentation import using_session
from openinference.instrumentation.langchain import LangChainInstrumentor
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor

SYSTEM = "You are a concise assistant. Use a tool or a skill when it helps, then answer in one or two sentences."
SKILL = """---
name: unit-converter
description: Convert between metric and imperial units. Use when the user asks to convert a length, weight or temperature.
---
# Unit converter
1. Name the two units and the value.
2. Use the calculate tool with these factors: 1 km = 0.621371 mi, 1 mi = 1.609344 km, 1 m = 3.28084 ft,
   1 kg = 2.20462 lb, C to F = C * 9 / 5 + 32.
3. Answer with the result rounded to two decimals and the factor you used.
"""
MEMORY = "# Notes\nThe user prefers metric units and short answers.\n"
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
    """A short fact sheet about a city."""
    return CITIES.get(city.lower(), f"no sheet for {city}")


@tool
def get_time(timezone: str = "UTC") -> str:
    """Current date and time in an IANA timezone, such as Asia/Tokyo."""
    try:
        return datetime.now(ZoneInfo(timezone)).isoformat(timespec="seconds")
    except Exception:
        return f"error: unknown timezone {timezone}"


@tool
def calculate(expression: str) -> str:
    """Evaluate an arithmetic expression such as (3 + 4) * 2."""
    try:
        return str(evaluate(ast.parse(expression, mode="eval")))
    except (ValueError, SyntaxError, ZeroDivisionError, OverflowError) as err:
        return f"error: {err}"  # the model reads it and tries again


async def main():
    if not os.environ.get("ANTHROPIC_API_KEY"):
        sys.exit("ANTHROPIC_API_KEY is not set")
    endpoint = os.environ.get("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318").rstrip("/")
    provider = TracerProvider(resource=Resource.create({"service.name": os.environ.get("OTEL_SERVICE_NAME", "bot-langchain-claude")}))
    provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter(endpoint=endpoint + "/v1/traces")))
    LangChainInstrumentor().instrument(tracer_provider=provider)

    root = Path(tempfile.mkdtemp(prefix="bot-langchain-"))
    (root / "skills" / "unit-converter").mkdir(parents=True)
    (root / "skills" / "unit-converter" / "SKILL.md").write_text(SKILL)
    (root / "AGENTS.md").write_text(MEMORY)

    tools = [city_info, get_time, calculate]
    if "--no-mcp" not in sys.argv:
        mcp = MultiServerMCPClient({"time": {"transport": "stdio", "command": "uvx", "args": ["mcp-server-time==2026.8.18"]}})
        tools += await mcp.get_tools()
    llm = ChatAnthropic(model=os.environ.get("MODEL", "claude-haiku-4-5"), max_tokens=2048, base_url=os.environ.get("ANTHROPIC_BASE_URL"))
    agent = create_deep_agent(
        model=llm, tools=tools, system_prompt=SYSTEM,
        skills=["/skills/"], memory=["/AGENTS.md"],
        subagents=[{"name": "researcher", "description": "Looks facts up with the city_info tool.", "system_prompt": "Answer from city_info only.", "tools": [city_info]}],
        backend=FilesystemBackend(root_dir=root, virtual_mode=True),
    )
    session, history = str(uuid.uuid4()), []

    async def turn(text):
        history.append({"role": "user", "content": text})
        with using_session(session):
            result = await agent.ainvoke({"messages": history})
        history[:] = result["messages"]
        provider.force_flush()
        return history[-1].text

    if "--once" in sys.argv:
        print(await turn(sys.argv[sys.argv.index("--once") + 1]))
        provider.shutdown()
        return
    print(f"langchain deep agents + claude, session {session[:8]}, {len(tools)} tools, 1 skill. /new, /quit.")
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
