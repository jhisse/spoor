#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["openai-agents==0.23.1", "openinference-instrumentation-openai-agents==2.5.2", "opentelemetry-sdk==1.45.0", "opentelemetry-exporter-otlp-proto-http==1.45.0"]
# [tool.uv]
# exclude-newer = "2026-10-05T03:23:43Z"
# ///
"""OpenAI Agents SDK on OpenAI or any compatible endpoint, traced by OpenInference.

Loads what the framework offers: function tools, an MCP server (uvx mcp-server-time), a skill loaded on
demand, a handoff to a second agent, an agent used as a tool, an input guardrail and a session.

    OPENAI_API_KEY=... [OPENAI_BASE_URL=https://openrouter.ai/api/v1] ./bot.py
    OPENAI_API_KEY=... ./bot.py --once "What is 12 * 12, and what is the time in Lisbon?"

Environment: OPENAI_API_KEY, OPENAI_BASE_URL (optional), MODEL (default gpt-4o-mini),
OTEL_EXPORTER_OTLP_ENDPOINT (default http://127.0.0.1:4318), OTEL_SERVICE_NAME. --no-mcp skips the MCP server.
"""
import ast
import asyncio
import contextlib
import operator
import os
import sys
import uuid
from datetime import datetime
from zoneinfo import ZoneInfo

from agents import (Agent, GuardrailFunctionOutput, InputGuardrailTripwireTriggered, Runner, SQLiteSession, function_tool, input_guardrail,
                    OpenAIChatCompletionsModel)
from agents.mcp import MCPServerStdio
from agents.tracing import set_trace_processors
from openai import AsyncOpenAI
from openinference.instrumentation import using_session
from openinference.instrumentation.openai_agents import OpenAIAgentsInstrumentor
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
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


@function_tool
def city_info(city: str) -> str:
    """A short fact sheet about a city."""
    return CITIES.get(city.lower(), f"no sheet for {city}")


@function_tool
def get_time(timezone: str = "UTC") -> str:
    """Current date and time in an IANA timezone, such as Asia/Tokyo."""
    try:
        return datetime.now(ZoneInfo(timezone)).isoformat(timespec="seconds")
    except Exception:
        return f"error: unknown timezone {timezone}"


@function_tool
def calculate(expression: str) -> str:
    """Evaluate an arithmetic expression such as (3 + 4) * 2."""
    try:
        return str(evaluate(ast.parse(expression, mode="eval")))
    except (ValueError, SyntaxError, ZeroDivisionError, OverflowError) as err:
        return f"error: {err}"  # the model reads it and tries again


@function_tool
def load_skill(name: str) -> str:
    """Load the instructions of a skill by name. Skills: unit-converter."""
    return SKILLS.get(name, f"no skill {name}; skills: {', '.join(SKILLS)}")


@input_guardrail
async def no_override(ctx, agent, user_input) -> GuardrailFunctionOutput:
    """Stops a prompt that tries to override the instructions."""
    text = user_input if isinstance(user_input, str) else str(user_input)
    return GuardrailFunctionOutput(output_info="override attempt", tripwire_triggered="ignore previous instructions" in text.lower())


async def main():
    key = os.environ.get("OPENAI_API_KEY")
    if not key:
        sys.exit("OPENAI_API_KEY is not set")
    endpoint = os.environ.get("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318").rstrip("/")
    provider = TracerProvider(resource=Resource.create({"service.name": os.environ.get("OTEL_SERVICE_NAME", "bot-openai-agents")}))
    provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter(endpoint=endpoint + "/v1/traces")))
    set_trace_processors([])  # the SDK's own exporter would send the traces to OpenAI
    OpenAIAgentsInstrumentor().instrument(tracer_provider=provider)
    # the model object, not its name: a name such as google/gemini-2.5-flash would be read as a provider prefix
    model = OpenAIChatCompletionsModel(model=os.environ.get("MODEL", "gpt-4o-mini"),
                                       openai_client=AsyncOpenAI(api_key=key, base_url=os.environ.get("OPENAI_BASE_URL")))

    async with contextlib.AsyncExitStack() as stack:
        mcp_servers = []
        if "--no-mcp" not in sys.argv:
            mcp_servers.append(await stack.enter_async_context(MCPServerStdio(params={"command": "uvx", "args": ["mcp-server-time==2026.8.18"]}, cache_tools_list=True)))
        calculator = Agent(name="calculator", handoff_description="Does arithmetic.", model=model,
                           instructions="Compute with the calculate tool and answer briefly.", tools=[calculate])
        researcher = Agent(name="researcher", model=model, instructions="Answer from city_info only.", tools=[city_info])
        assistant = Agent(
            name="assistant", model=model, mcp_servers=mcp_servers, handoffs=[calculator], input_guardrails=[no_override],
            instructions="You are a concise assistant. Use a tool when it helps, then answer in one or two sentences. "
                         "Skills you can load: " + ", ".join(SKILLS) + ". Hand arithmetic to the calculator.",
            tools=[city_info, get_time, load_skill, researcher.as_tool("ask_researcher", "Ask the researcher about a city.")])
        session = str(uuid.uuid4())
        memory = SQLiteSession(session)  # in memory

        async def turn(text):
            try:
                with using_session(session):
                    result = await Runner.run(assistant, text, session=memory)
                reply = str(result.final_output)
            except InputGuardrailTripwireTriggered:
                reply = "(stopped by the input guardrail)"
            provider.force_flush()
            return reply

        if "--once" in sys.argv:
            print(await turn(sys.argv[sys.argv.index("--once") + 1]))
            return
        print(f"openai agents sdk, session {session[:8]}. /new, /quit.")
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
                session = str(uuid.uuid4())
                memory = SQLiteSession(session)
                print(f"session {session[:8]}")
            elif text:
                print("bot>", await turn(text))


if __name__ == "__main__":
    asyncio.run(main())
