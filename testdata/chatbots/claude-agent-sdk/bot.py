#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["claude-agent-sdk==0.2.163"]
# [tool.uv]
# exclude-newer = "2026-10-05T03:23:43Z"
# ///
"""Claude Agent SDK, with the telemetry Claude Code itself exports (no instrumentation library).

Loads what the framework offers: a skill (SKILL.md under .claude/skills), in-process tools, an MCP
server (uvx mcp-server-time), a subagent and a hook. The built-in file and shell tools are off.

    ANTHROPIC_API_KEY=... ./bot.py
    ANTHROPIC_API_KEY=... ./bot.py --once "How many feet in 3 km?"
    ./bot.py --selftest        # no model call needed: prints the skills, tools and MCP servers the session loaded

Environment: ANTHROPIC_API_KEY, MODEL (optional), ANTHROPIC_BASE_URL (optional),
OTEL_EXPORTER_OTLP_ENDPOINT (default http://127.0.0.1:4318). --no-mcp skips the MCP server.
"""
import ast
import asyncio
import operator
import os
import sys
import tempfile
from datetime import datetime
from pathlib import Path
from zoneinfo import ZoneInfo

from claude_agent_sdk import (AgentDefinition, AssistantMessage, ClaudeAgentOptions, ClaudeSDKClient, HookMatcher, ResultMessage,
                              SystemMessage, TextBlock, create_sdk_mcp_server, tool)

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


def text(s: str) -> dict:
    return {"content": [{"type": "text", "text": s}]}


@tool("city_info", "A short fact sheet about a city.", {"city": str})
async def city_info(args):
    return text(CITIES.get(args["city"].lower(), f"no sheet for {args['city']}"))


@tool("get_time", "Current date and time in an IANA timezone, such as Asia/Tokyo.", {"timezone": str})
async def get_time(args):
    try:
        return text(datetime.now(ZoneInfo(args["timezone"])).isoformat(timespec="seconds"))
    except Exception:
        return text(f"error: unknown timezone {args['timezone']}")


@tool("calculate", "Evaluate an arithmetic expression such as (3 + 4) * 2.", {"expression": str})
async def calculate(args):
    try:
        return text(str(evaluate(ast.parse(args["expression"], mode="eval"))))
    except (ValueError, SyntaxError, ZeroDivisionError, OverflowError) as err:
        return text(f"error: {err}")


async def log_tool(hook_input, tool_use_id, context):
    print(f"  [hook] {hook_input.get('tool_name')}", file=sys.stderr)
    return {}


async def main():
    selftest = "--selftest" in sys.argv
    if not selftest and not os.environ.get("ANTHROPIC_API_KEY"):
        sys.exit("ANTHROPIC_API_KEY is not set")
    endpoint = os.environ.get("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318").rstrip("/")
    home = Path(tempfile.mkdtemp(prefix="bot-claude-agent-"))
    (home / ".claude" / "skills" / "unit-converter").mkdir(parents=True)
    (home / ".claude" / "skills" / "unit-converter" / "SKILL.md").write_text(SKILL)

    servers = {"tools": create_sdk_mcp_server(name="tools", version="1.0.0", tools=[city_info, get_time, calculate])}
    if "--no-mcp" not in sys.argv:
        servers["time"] = {"command": "uvx", "args": ["mcp-server-time==2026.8.18"]}
    options = ClaudeAgentOptions(
        cwd=str(home), setting_sources=["project"], skills=["unit-converter"],
        tools=["Skill", "Task"], allowed_tools=["Skill", "Task", "mcp__tools__city_info", "mcp__tools__get_time", "mcp__tools__calculate", "mcp__time__get_current_time", "mcp__time__convert_time"],
        mcp_servers=servers, strict_mcp_config=True, model=os.environ.get("MODEL"),
        system_prompt="You are a concise assistant. Use a tool or a skill when it helps, then answer in one or two sentences.",
        agents={"researcher": AgentDefinition(description="Looks up facts about cities.", prompt="Answer from city_info only.", tools=["mcp__tools__city_info"])},
        hooks={"PreToolUse": [HookMatcher(hooks=[log_tool])]},
        env={  # what Claude Code itself exports; spoor reads it as it reads a terminal session
            "CLAUDE_CODE_ENABLE_TELEMETRY": "1", "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "1",
            "OTEL_TRACES_EXPORTER": "otlp", "OTEL_METRICS_EXPORTER": "none", "OTEL_LOGS_EXPORTER": "none",
            "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL": "http/protobuf", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": endpoint + "/v1/traces",
            "OTEL_LOG_USER_PROMPTS": "1", "OTEL_LOG_TOOL_DETAILS": "1", "OTEL_SERVICE_NAME": os.environ.get("OTEL_SERVICE_NAME", "bot-claude-agent-sdk"),
        })

    async def ask(client, prompt):
        await client.query(prompt)
        reply = ""
        async for message in client.receive_response():
            if isinstance(message, SystemMessage) and message.subtype == "init":
                d = message.data
                print(f"  loaded: skills {d.get('skills')}, agents {d.get('agents')}, mcp {[s.get('name') for s in d.get('mcp_servers', [])]}, {len(d.get('tools', []))} tools", file=sys.stderr)
            elif isinstance(message, AssistantMessage):
                reply += "".join(b.text for b in message.content if isinstance(b, TextBlock))
            elif isinstance(message, ResultMessage) and message.is_error:
                reply += f"(error: {message.result})"
        return reply

    async with ClaudeSDKClient(options=options) as client:
        if selftest:
            print(await ask(client, "hi"))
        elif "--once" in sys.argv:
            print(await ask(client, sys.argv[sys.argv.index("--once") + 1]))
        else:
            print("claude agent sdk. /quit to leave (one session; /new is a restart).")
            while True:
                try:
                    line = (await asyncio.to_thread(input, "you> ")).strip()
                except EOFError:
                    break
                if not sys.stdin.isatty():  # a piped session: show what was typed
                    print(line)
                if line == "/quit":
                    break
                if line:
                    print("bot>", await ask(client, line))


if __name__ == "__main__":
    asyncio.run(main())
