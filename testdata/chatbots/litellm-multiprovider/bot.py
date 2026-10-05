#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["litellm==1.104.0", "mcp==2.3.0", "opentelemetry-sdk==1.45.0", "opentelemetry-exporter-otlp-proto-http==1.45.0"]
# [tool.uv]
# exclude-newer = "2026-10-05T03:23:43Z"
# ///
"""LiteLLM Router over whichever provider keys are set, with fallbacks, traced by LiteLLM's own OpenTelemetry callback.

Loads what the framework offers: a Router with retries and fallbacks across providers, function tools,
an MCP server (uvx mcp-server-time) through LiteLLM's MCP client, and a skill loaded on demand.
The tool loop is written out here, as LiteLLM has none.

    ANTHROPIC_API_KEY=... OPENAI_API_KEY=... ./bot.py      # the first key found is tried first, the others are fallbacks
    MODEL=groq/llama-3.3-70b-versatile GROQ_API_KEY=... ./bot.py --once "What is 12 * 12?"

Keys looked for, in this order: ANTHROPIC_API_KEY, OPENAI_API_KEY (+ OPENAI_BASE_URL), GEMINI_API_KEY, GROQ_API_KEY,
MISTRAL_API_KEY, DEEPSEEK_API_KEY, OPENROUTER_API_KEY. MODEL forces one litellm model string.
Also: OTEL_EXPORTER_OTLP_ENDPOINT (default http://127.0.0.1:4318). --no-mcp skips the MCP server.
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

endpoint = os.environ.get("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318").rstrip("/")
os.environ["OTEL_EXPORTER"] = "otlp_http"
os.environ["OTEL_ENDPOINT"] = endpoint + "/v1/traces"
os.environ.setdefault("OTEL_SERVICE_NAME", "bot-litellm-multiprovider")

import litellm
from litellm.experimental_mcp_client import load_mcp_tools
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

PROVIDERS = [  # (key variable, litellm model, extra params)
    ("ANTHROPIC_API_KEY", "anthropic/claude-haiku-4-5", {}),
    ("OPENAI_API_KEY", "openai/gpt-4o-mini", {"api_base": os.environ.get("OPENAI_BASE_URL")}),
    ("GEMINI_API_KEY", "gemini/gemini-2.5-flash", {}),
    ("GROQ_API_KEY", "groq/llama-3.3-70b-versatile", {}),
    ("MISTRAL_API_KEY", "mistral/mistral-small-latest", {}),
    ("DEEPSEEK_API_KEY", "deepseek/deepseek-chat", {}),
    ("OPENROUTER_API_KEY", "openrouter/anthropic/claude-haiku-4-5", {}),
]
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


def model_list():
    found = [(v, m, p) for v, m, p in PROVIDERS if os.environ.get(v)]
    if forced := os.environ.get("MODEL"):
        found = [(v, forced, p) for v, _, p in found[:1]] or [("", forced, {})]
    if not found:
        sys.exit("set one of: " + ", ".join(v for v, _, _ in PROVIDERS))
    return [{"model_name": f"m{i}", "litellm_params": {"model": m, "api_key": os.environ.get(v), **{k: x for k, x in p.items() if x}}} for i, (v, m, p) in enumerate(found)]


async def main():
    litellm.callbacks = ["otel"]
    models = model_list()
    router = litellm.Router(model_list=models, num_retries=1,
                            fallbacks=[{models[0]["model_name"]: [m["model_name"] for m in models[1:]]}] if len(models) > 1 else [])
    session = str(uuid.uuid4())
    history = [{"role": "system", "content": "You are a concise assistant. Use a tool when it helps, then answer in one or two sentences. "
                                              "Skills you can load: " + ", ".join(SKILLS) + "."}]
    async with contextlib.AsyncExitStack() as stack:
        mcp_session, mcp_tools = None, []
        if "--no-mcp" not in sys.argv:
            read, write = await stack.enter_async_context(stdio_client(StdioServerParameters(command="uvx", args=["mcp-server-time==2026.8.18"])))
            mcp_session = await stack.enter_async_context(ClientSession(read, write))
            await mcp_session.initialize()
            mcp_tools = await load_mcp_tools(session=mcp_session, format="openai")
        specs = LOCAL_SPECS + mcp_tools

        async def turn(text):
            history.append({"role": "user", "content": text})
            for _ in range(6):
                reply = await router.acompletion(model=models[0]["model_name"], messages=history, tools=specs, litellm_session_id=session)
                message = reply.choices[0].message
                history.append(message.model_dump(exclude_none=True))
                if not message.tool_calls:
                    return message.content or ""
                for call in message.tool_calls:
                    args = json.loads(call.function.arguments or "{}")
                    if call.function.name in LOCAL:
                        out = LOCAL[call.function.name](**args)
                    else:
                        result = await mcp_session.call_tool(call.function.name, args)
                        out = "".join(getattr(c, "text", "") for c in result.content)
                    history.append({"role": "tool", "tool_call_id": call.id, "content": str(out)})
            return "(stopped after 6 model calls)"

        if "--once" in sys.argv:
            print(await turn(sys.argv[sys.argv.index("--once") + 1]))
            return
        print(f"litellm, {', '.join(m['litellm_params']['model'] for m in models)}, session {session[:8]}. /new, /quit.")
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
                print("bot>", await turn(text))


if __name__ == "__main__":
    asyncio.run(main())
