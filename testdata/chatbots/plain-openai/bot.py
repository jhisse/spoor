#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["openai==3.24.0", "httpx==0.28.1", "mcp==2.3.0", "opentelemetry-instrumentation-openai-v2==2.4b0", "opentelemetry-util-genai==1.1b0", "opentelemetry-sdk==1.45.0", "opentelemetry-exporter-otlp-proto-http==1.45.0"]
# [tool.uv]
# exclude-newer = "2026-10-05T03:23:43Z"
# ///
"""The OpenAI SDK with no framework, streaming, on OpenAI or any compatible endpoint: the official OpenTelemetry
instrumentation for the model calls and hand-written GenAI-convention spans for the turn and the tools.

Loads: function tools, an MCP server (uvx mcp-server-time) through the MCP client, and a skill loaded on demand.

    OPENAI_API_KEY=... [OPENAI_BASE_URL=https://openrouter.ai/api/v1] ./bot.py
    OPENAI_API_KEY=... ./bot.py --once "What is 12 * 12?"

Environment: OPENAI_API_KEY, OPENAI_BASE_URL (optional), MODEL (default gpt-4o-mini),
OTEL_EXPORTER_OTLP_ENDPOINT (default http://127.0.0.1:4318), OTEL_SERVICE_NAME. --no-stream, --no-mcp.
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

os.environ.setdefault("OTEL_SEMCONV_STABILITY_OPT_IN", "gen_ai_latest_experimental")
os.environ.setdefault("OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT", "span_only")

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client
from openai import AsyncOpenAI
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.instrumentation.openai_v2 import OpenAIInstrumentor
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
    return {"type": "function", "function": {"name": fn, "description": description,
            "parameters": {"type": "object", "properties": {k: {"type": "string"} for k in params}, "required": list(params)}}}


LOCAL = {"city_info": city_info, "get_time": get_time, "calculate": calculate, "load_skill": load_skill}
LOCAL_SPECS = [spec("city_info", "A short fact sheet about a city.", "city"), spec("get_time", "Current time in an IANA timezone.", "timezone"),
               spec("calculate", "Evaluate an arithmetic expression such as (3 + 4) * 2.", "expression"),
               spec("load_skill", "Load the instructions of a skill by name. Skills: " + ", ".join(SKILLS), "name")]


async def main():
    key = os.environ.get("OPENAI_API_KEY")
    if not key:
        sys.exit("OPENAI_API_KEY is not set")
    endpoint = os.environ.get("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318").rstrip("/")
    provider = TracerProvider(resource=Resource.create({"service.name": os.environ.get("OTEL_SERVICE_NAME", "bot-plain-openai")}))
    provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter(endpoint=endpoint + "/v1/traces")))
    trace.set_tracer_provider(provider)
    OpenAIInstrumentor().instrument(tracer_provider=provider)
    tracer = trace.get_tracer("bot-plain-openai")

    client = AsyncOpenAI(api_key=key, base_url=os.environ.get("OPENAI_BASE_URL"))
    model = os.environ.get("MODEL", "gpt-4o-mini")
    stream = "--no-stream" not in sys.argv and "--once" not in sys.argv
    session = str(uuid.uuid4())
    history = [{"role": "system", "content": "You are a concise assistant. Use a tool when it helps, then answer in one or two sentences. "
                                              "Skills you can load: " + ", ".join(SKILLS) + "."}]

    async with contextlib.AsyncExitStack() as stack:
        mcp, specs = None, list(LOCAL_SPECS)
        if "--no-mcp" not in sys.argv:
            read, write = await stack.enter_async_context(stdio_client(StdioServerParameters(command="uvx", args=["mcp-server-time==2026.8.18"])))
            mcp = await stack.enter_async_context(ClientSession(read, write))
            await mcp.initialize()
            specs += [{"type": "function", "function": {"name": t.name, "description": t.description or "", "parameters": t.input_schema}}
                      for t in (await mcp.list_tools()).tools]

        async def run_tool(call):
            args = json.loads(call.function.arguments or "{}")
            with tracer.start_as_current_span(f"execute_tool {call.function.name}", attributes={
                    "gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": call.function.name, "gen_ai.tool.call.id": call.id,
                    "gen_ai.tool.call.arguments": call.function.arguments or "{}"}) as span:
                if call.function.name in LOCAL:
                    out = LOCAL[call.function.name](**args)
                else:
                    result = await mcp.call_tool(call.function.name, args)
                    out = "".join(getattr(c, "text", "") for c in result.content)
                span.set_attribute("gen_ai.tool.call.result", str(out))
                return str(out)

        async def complete():
            if not stream:
                return (await client.chat.completions.create(model=model, messages=history, tools=specs)).choices[0].message.model_dump(exclude_none=True)
            parts, calls = [], {}
            async for chunk in await client.chat.completions.create(model=model, messages=history, tools=specs, stream=True, stream_options={"include_usage": True}):
                for choice in chunk.choices:
                    if choice.delta.content:
                        parts.append(choice.delta.content)
                        print(choice.delta.content, end="", flush=True)
                    for t in choice.delta.tool_calls or []:
                        c = calls.setdefault(t.index, {"id": "", "type": "function", "function": {"name": "", "arguments": ""}})
                        c["id"] = t.id or c["id"]
                        c["function"]["name"] += t.function.name or ""
                        c["function"]["arguments"] += t.function.arguments or ""
            message = {"role": "assistant", "content": "".join(parts) or None}
            if calls:
                message["tool_calls"] = list(calls.values())
            return message

        async def turn(text):
            history.append({"role": "user", "content": text})
            reply = ""
            with tracer.start_as_current_span("invoke_agent plain-openai", attributes={
                    "gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "plain-openai",
                    "gen_ai.conversation.id": session, "session.id": session}):
                for _ in range(6):
                    message = await complete()
                    history.append(message)
                    reply = message.get("content") or ""
                    if not message.get("tool_calls"):
                        break
                    for call in (type("C", (), {"id": c["id"], "function": type("F", (), c["function"])}) for c in message["tool_calls"]):
                        history.append({"role": "tool", "tool_call_id": call.id, "content": await run_tool(call)})
            provider.force_flush()
            return reply

        if "--once" in sys.argv:
            print(await turn(sys.argv[sys.argv.index("--once") + 1]))
            return
        print(f"openai sdk ({'streaming' if stream else 'no streaming'}), session {session[:8]}, {len(specs)} tools. /new, /quit.")
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
                session, history[:] = str(uuid.uuid4()), history[:1]
                print(f"session {session[:8]}")
            elif text:
                print("bot> ", end="")
                reply = await turn(text)
                print("" if stream else reply)


if __name__ == "__main__":
    asyncio.run(main())
