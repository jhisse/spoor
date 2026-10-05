#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["llama-index-core==0.14.25", "llama-index-llms-openai-like==0.8.1", "llama-index-tools-mcp==0.6.0", "traceloop-sdk==0.62.4"]
# [tool.uv]
# exclude-newer = "2026-10-05T03:23:43Z"
# ///
"""LlamaIndex multi-agent workflow on any OpenAI-compatible endpoint, traced by OpenLLMetry (traceloop-sdk).

Loads what the framework offers: function tools, a retriever tool over a keyword index, MCP tools
(uvx mcp-server-time), a skill loaded on demand, and a handoff between two agents.

    OPENAI_API_KEY=... [OPENAI_BASE_URL=https://openrouter.ai/api/v1] ./bot.py
    OPENAI_API_KEY=... ./bot.py --once "What is 17 * 23, and what time is it in Tokyo?"

Environment: OPENAI_API_KEY, OPENAI_BASE_URL (default https://api.openai.com/v1), MODEL (default gpt-4o-mini),
OTEL_EXPORTER_OTLP_ENDPOINT (default http://127.0.0.1:4318), OTEL_SERVICE_NAME. --no-mcp skips the MCP server.
"""
import ast
import asyncio
import operator
import os
import sys
import uuid
from datetime import datetime
from zoneinfo import ZoneInfo

endpoint = os.environ.get("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318").rstrip("/")
os.environ.setdefault("TRACELOOP_BASE_URL", endpoint)
os.environ.setdefault("TRACELOOP_TELEMETRY", "false")

from llama_index.core import Document
from llama_index.core.agent.workflow import AgentWorkflow, FunctionAgent
from llama_index.core.indices.keyword_table import SimpleKeywordTableIndex
from llama_index.core.tools import FunctionTool, RetrieverTool
from llama_index.core.workflow import Context
from llama_index.llms.openai_like import OpenAILike
from llama_index.tools.mcp import BasicMCPClient, McpToolSpec
from traceloop.sdk import Traceloop
from traceloop.sdk.decorators import workflow as traced_workflow

SYSTEM = "You are a concise assistant. Use a tool when it helps, then answer in one or two sentences."
SKILLS = {
    "unit-converter": "Convert metric and imperial units with the calculate tool. 1 km = 0.621371 mi, 1 m = 3.28084 ft, "
                      "1 kg = 2.20462 lb, C to F = C * 9 / 5 + 32. Answer rounded to two decimals, naming the factor.",
}
DOCS = [
    "Paris is the capital of France. The Louvre opens at nine and closes at six.",
    "Tokyo is the capital of Japan. Trains run until midnight and start again at five.",
    "Lisbon is the capital of Portugal. Tram 28 crosses the old town.",
]
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
    """A short fact sheet about a city."""
    return CITIES.get(city.lower(), f"no sheet for {city}")


def get_time(timezone: str = "UTC") -> str:
    """Current date and time in an IANA timezone, such as Asia/Tokyo."""
    try:
        return datetime.now(ZoneInfo(timezone)).isoformat(timespec="seconds")
    except Exception:
        return f"error: unknown timezone {timezone}"


def calculate(expression: str) -> str:
    """Evaluate an arithmetic expression such as (3 + 4) * 2."""
    try:
        return str(evaluate(ast.parse(expression, mode="eval")))
    except (ValueError, SyntaxError, ZeroDivisionError, OverflowError) as err:
        return f"error: {err}"  # the model reads it and tries again


def load_skill(name: str) -> str:
    """Load the instructions of a skill by name. Skills: """ + ", ".join(SKILLS)
    return SKILLS.get(name, f"no skill {name}; skills: {', '.join(SKILLS)}")


async def main():
    key = os.environ.get("OPENAI_API_KEY")
    if not key:
        sys.exit("OPENAI_API_KEY is not set")
    Traceloop.init(app_name=os.environ.get("OTEL_SERVICE_NAME", "bot-llamaindex-compatible"), disable_batch=True)

    llm = OpenAILike(model=os.environ.get("MODEL", "gpt-4o-mini"), api_base=os.environ.get("OPENAI_BASE_URL", "https://api.openai.com/v1"),
                     api_key=key, is_chat_model=True, is_function_calling_model=True)
    retriever = SimpleKeywordTableIndex.from_documents([Document(text=t) for t in DOCS], llm=llm).as_retriever()
    tools = [FunctionTool.from_defaults(f) for f in (city_info, get_time, load_skill)] + [
        RetrieverTool.from_defaults(retriever, name="travel_notes", description="Search short travel notes about cities.")]
    if "--no-mcp" not in sys.argv:
        tools += await McpToolSpec(BasicMCPClient("uvx", args=["mcp-server-time==2026.8.18"])).to_tool_list_async()
    assistant = FunctionAgent(name="assistant", description="Answers questions, uses tools, hands arithmetic to the calculator.",
                              system_prompt=SYSTEM + " Skills you can load: " + ", ".join(SKILLS) + ".",
                              llm=llm, tools=tools, can_handoff_to=["calculator"], streaming=False)
    calculator = FunctionAgent(name="calculator", description="Does arithmetic.", system_prompt="Compute with the calculate tool and answer briefly.",
                               llm=llm, tools=[FunctionTool.from_defaults(calculate)], streaming=False)
    workflow = AgentWorkflow(agents=[assistant, calculator], root_agent="assistant")
    session = str(uuid.uuid4())
    ctx = Context(workflow)

    @traced_workflow(name="chat_turn")  # one parent span per turn: the workflow's own steps would otherwise be separate traces
    async def turn(text):
        Traceloop.set_association_properties({"session_id": session})
        return str(await workflow.run(user_msg=text, ctx=ctx))

    if "--once" in sys.argv:
        print(await turn(sys.argv[sys.argv.index("--once") + 1]))
        return
    print(f"llamaindex + openai-compatible, session {session[:8]}, {len(tools) + 1} tools, 2 agents. /new, /quit.")
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
            session, ctx = str(uuid.uuid4()), Context(workflow)
            print(f"session {session[:8]}")
        elif text:
            print("bot>", await turn(text))


if __name__ == "__main__":
    asyncio.run(main())
