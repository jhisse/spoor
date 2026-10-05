#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# ///
"""A fake model provider that streams. No key, no network; standard library only.

POST /v1/chat/completions   OpenAI chat completions, streamed or not
POST /v1/messages           Anthropic messages, streamed or not (Claude Code streams)
POST /v1/messages/count_tokens

A request that offers tools and carries no tool result yet gets a call to the first tool named like
city_info (else the first tool), with {"city": "Paris"}; after a result it answers "Paris." Usage is fixed:
1200 prompt tokens, 1000 of them cached, 40 output. Run it with:  ./fake_stream.py [port]   (default 14332)
"""
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

USAGE_IN, CACHED, USAGE_OUT = 1200, 1000, 40


def pick(names):
    return next((n for n in names if "city_info" in n), names[0])


def anthropic_plan(req):
    tools = [t["name"] for t in req.get("tools") or []]
    last = (req["messages"] or [{}])[-1]
    result = isinstance(last.get("content"), list) and any(b.get("type") == "tool_result" for b in last["content"])
    if tools and not result and "city_info" in " ".join(tools):
        return {"tool": pick(tools), "input": {"city": "Paris"}}
    return {"text": "Paris."}


def openai_plan(req):
    tools = [t["function"]["name"] for t in req.get("tools") or []]
    if tools and req["messages"][-1]["role"] != "tool":
        return {"tool": pick(tools), "input": {"city": "Paris"}}
    return {"text": "Paris."}


def sse(event, data):
    return (f"event: {event}\n" if event else "") + "data: " + json.dumps(data) + "\n\n"


def anthropic_stream(req, plan):
    usage = {"input_tokens": USAGE_IN - CACHED, "cache_read_input_tokens": CACHED, "cache_creation_input_tokens": 0, "output_tokens": 1}
    out = [sse("message_start", {"type": "message_start", "message": {"id": "msg_fake", "type": "message", "role": "assistant", "model": req["model"],
                                                                      "content": [], "stop_reason": None, "stop_sequence": None, "usage": usage}})]
    if "tool" in plan:
        block, delta, stop = {"type": "tool_use", "id": "toolu_fake", "name": plan["tool"], "input": {}}, {"type": "input_json_delta", "partial_json": json.dumps(plan["input"])}, "tool_use"
    else:
        block, delta, stop = {"type": "text", "text": ""}, {"type": "text_delta", "text": plan["text"]}, "end_turn"
    out += [sse("content_block_start", {"type": "content_block_start", "index": 0, "content_block": block}),
            sse("content_block_delta", {"type": "content_block_delta", "index": 0, "delta": delta}),
            sse("content_block_stop", {"type": "content_block_stop", "index": 0}),
            sse("message_delta", {"type": "message_delta", "delta": {"stop_reason": stop, "stop_sequence": None}, "usage": {"output_tokens": USAGE_OUT}}),
            sse("message_stop", {"type": "message_stop"})]
    return "".join(out).encode()


def anthropic_json(req, plan):
    content = [{"type": "tool_use", "id": "toolu_fake", "name": plan["tool"], "input": plan["input"]}] if "tool" in plan else [{"type": "text", "text": plan["text"]}]
    return json.dumps({"id": "msg_fake", "type": "message", "role": "assistant", "model": req["model"], "content": content,
                       "stop_reason": "tool_use" if "tool" in plan else "end_turn", "stop_sequence": None,
                       "usage": {"input_tokens": USAGE_IN - CACHED, "cache_read_input_tokens": CACHED, "cache_creation_input_tokens": 0, "output_tokens": USAGE_OUT}}).encode()


def openai_usage():
    return {"prompt_tokens": USAGE_IN, "completion_tokens": USAGE_OUT, "total_tokens": USAGE_IN + USAGE_OUT,
            "prompt_tokens_details": {"cached_tokens": CACHED}}


def openai_stream(req, plan):
    def chunk(delta, finish=None, usage=None):
        return sse(None, {"id": "chatcmpl-fake", "object": "chat.completion.chunk", "created": 1, "model": req["model"],
                          "choices": [] if usage else [{"index": 0, "delta": delta, "finish_reason": finish}], **({"usage": usage} if usage else {})})
    if "tool" in plan:
        call = {"index": 0, "id": "call_fake", "type": "function", "function": {"name": plan["tool"], "arguments": json.dumps(plan["input"])}}
        parts = [chunk({"role": "assistant", "content": None, "tool_calls": [call]}), chunk({}, "tool_calls")]
    else:
        parts = [chunk({"role": "assistant", "content": plan["text"]}), chunk({}, "stop")]
    if (req.get("stream_options") or {}).get("include_usage"):
        parts.append(chunk(None, usage=openai_usage()))
    return ("".join(parts) + "data: [DONE]\n\n").encode()


def openai_json(req, plan):
    if "tool" in plan:
        msg = {"role": "assistant", "content": None, "tool_calls": [{"id": "call_fake", "type": "function", "function": {"name": plan["tool"], "arguments": json.dumps(plan["input"])}}]}
    else:
        msg = {"role": "assistant", "content": plan["text"]}
    return json.dumps({"id": "chatcmpl-fake", "object": "chat.completion", "created": 1, "model": req["model"],
                       "choices": [{"index": 0, "message": msg, "finish_reason": "tool_calls" if "tool" in plan else "stop"}], "usage": openai_usage()}).encode()


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        req = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or b"{}")
        path, stream = self.path.split("?")[0], bool(req.get("stream"))
        if path.endswith("/count_tokens"):
            out, ctype = json.dumps({"input_tokens": USAGE_IN}).encode(), "application/json"
        elif path.endswith("/messages"):
            plan = anthropic_plan(req)
            out, ctype = (anthropic_stream(req, plan), "text/event-stream") if stream else (anthropic_json(req, plan), "application/json")
        elif path.endswith("/chat/completions"):
            plan = openai_plan(req)
            out, ctype = (openai_stream(req, plan), "text/event-stream") if stream else (openai_json(req, plan), "application/json")
        else:
            self.send_error(404)
            return
        self.send_response(200)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(out)))
        self.end_headers()
        self.wfile.write(out)

    def log_message(self, fmt, *args):
        print(self.command, self.path, file=sys.stderr)


if __name__ == "__main__":
    ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1]) if len(sys.argv) > 1 else 14332), Handler).serve_forever()
