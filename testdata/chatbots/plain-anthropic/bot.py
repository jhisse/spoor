#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["anthropic==1.11.0", "mcp==2.3.0", "opentelemetry-instrumentation-anthropic==0.62.4", "opentelemetry-sdk==1.45.0", "opentelemetry-exporter-otlp-proto-http==1.45.0"]
# [tool.uv]
# exclude-newer = "2026-10-05T03:23:43Z"
# ///
"""The Anthropic SDK with no framework: OpenLLMetry's instrumentation for the model calls and hand-written
GenAI-convention spans for the turn and the tools.

Loads: function tools, an MCP server (uvx mcp-server-time) through the MCP client, and a skill loaded on demand.

    ANTHROPIC_API_KEY=... ./bot.py
    ANTHROPIC_API_KEY=... ./bot.py --once "What is 12 * 12?"

Environment: ANTHROPIC_API_KEY, MODEL (default claude-haiku-4-5), THINKING_TOKENS (extended thinking budget), ANTHROPIC_BASE_URL (optional),
OTEL_EXPORTER_OTLP_ENDPOINT (default http://127.0.0.1:4318), OTEL_SERVICE_NAME. --no-mcp skips the MCP server.
"""
import ast
import asyncio
import contextlib
import json
import operator
import os
import sys
import uuid
from datetime import datetime
from zoneinfo import ZoneInfo

from anthropic import AsyncAnthropic
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.instrumentation.anthropic import AnthropicInstrumentor
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor

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


def city_info(city: str) -> str:
    return CITIES.get(city.lower(), f"no sheet for {city}")


def get_time(timezone: str = "UTC") -> str:
    try:
        return datetime.now(ZoneInfo(timezone)).isoformat(timespec="seconds")
    except Exception:
        return f"error: unknown timezone {timezone}"


def calculate(expression: str) -> str:
    try:
        return str(evaluate(ast.parse(expression, mode="eval")))
    except (ValueError, SyntaxError, ZeroDivisionError, OverflowError) as err:
        return f"error: {err}"


def load_skill(name: str) -> str:
    return SKILLS.get(name, f"no skill {name}; skills: {', '.join(SKILLS)}")


def spec(fn, description, *params):
    return {"name": fn, "description": description,
            "input_schema": {"type": "object", "properties": {k: {"type": "string"} for k in params}, "required": list(params)}}


LOCAL = {"city_info": city_info, "get_time": get_time, "calculate": calculate, "load_skill": load_skill}
LOCAL_SPECS = [spec("city_info", "A short fact sheet about a city.", "city"), spec("get_time", "Current time in an IANA timezone.", "timezone"),
               spec("calculate", "Evaluate an arithmetic expression such as (3 + 4) * 2.", "expression"),
               spec("load_skill", "Load the instructions of a skill by name. Skills: " + ", ".join(SKILLS), "name")]


async def main():
    if not os.environ.get("ANTHROPIC_API_KEY"):
        sys.exit("ANTHROPIC_API_KEY is not set")
    endpoint = os.environ.get("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318").rstrip("/")
    provider = TracerProvider(resource=Resource.create({"service.name": os.environ.get("OTEL_SERVICE_NAME", "bot-plain-anthropic")}))
    provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter(endpoint=endpoint + "/v1/traces")))
    trace.set_tracer_provider(provider)
    AnthropicInstrumentor().instrument()
    tracer = trace.get_tracer("bot-plain-anthropic")

    client = AsyncAnthropic(base_url=os.environ.get("ANTHROPIC_BASE_URL"))
    model = os.environ.get("MODEL", "claude-haiku-4-5")
    thinking = int(os.environ.get("THINKING_TOKENS", "0"))  # a budget turns extended thinking on (at least 1024)
    system = "You are a concise assistant. Use a tool when it helps, then answer in one or two sentences. Skills you can load: " + ", ".join(SKILLS) + "."
    session, history = str(uuid.uuid4()), []

    async with contextlib.AsyncExitStack() as stack:
        mcp, specs = None, list(LOCAL_SPECS)
        if "--no-mcp" not in sys.argv:
            read, write = await stack.enter_async_context(stdio_client(StdioServerParameters(command="uvx", args=["mcp-server-time==2026.8.18"])))
            mcp = await stack.enter_async_context(ClientSession(read, write))
            await mcp.initialize()
            specs += [{"name": t.name, "description": t.description or "", "input_schema": t.input_schema} for t in (await mcp.list_tools()).tools]

        async def run_tool(call):
            with tracer.start_as_current_span(f"execute_tool {call.name}", attributes={
                    "gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": call.name, "gen_ai.tool.call.id": call.id,
                    "gen_ai.tool.call.arguments": json.dumps(call.input)}) as span:
                if call.name in LOCAL:
                    out = LOCAL[call.name](**call.input)
                else:
                    result = await mcp.call_tool(call.name, call.input)
                    out = "".join(getattr(c, "text", "") for c in result.content)
                span.set_attribute("gen_ai.tool.call.result", str(out))
                return str(out)

        async def turn(text):
            history.append({"role": "user", "content": text})
            with tracer.start_as_current_span("invoke_agent plain-anthropic", attributes={
                    "gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "plain-anthropic",
                    "gen_ai.conversation.id": session, "session.id": session}):
                for _ in range(6):
                    reply = await client.messages.create(model=model, max_tokens=1024 + thinking, system=system, tools=specs, messages=history,
                                                         **({"thinking": {"type": "enabled", "budget_tokens": thinking}} if thinking else {}))
                    history.append({"role": "assistant", "content": [b.model_dump(exclude_none=True) for b in reply.content]})
                    calls = [b for b in reply.content if b.type == "tool_use"]
                    if not calls:
                        break
                    results = [{"type": "tool_result", "tool_use_id": c.id, "content": await run_tool(c)} for c in calls]
                    history.append({"role": "user", "content": results})
            provider.force_flush()
            return "".join(b.text for b in reply.content if b.type == "text")

        if "--once" in sys.argv:
            print(await turn(sys.argv[sys.argv.index("--once") + 1]))
            return
        print(f"anthropic sdk, session {session[:8]}, {len(specs)} tools. /new, /quit.")
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
                session, history[:] = str(uuid.uuid4()), []
                print(f"session {session[:8]}")
            elif text:
                print("bot>", await turn(text))


if __name__ == "__main__":
    asyncio.run(main())
