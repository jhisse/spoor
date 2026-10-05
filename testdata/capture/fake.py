# /// script
# requires-python = ">=3.12"
# dependencies = ["opentelemetry-proto==1.45.0"]
# [tool.uv]
# exclude-newer = "2026-10-05T03:23:43Z"
# ///
"""A fake model provider and an OTLP sink on one port. No key, no network.

POST /v1/chat/completions   OpenAI chat completions (canned)
POST /v1/messages           Anthropic messages (canned)
POST /<name>/v1/traces      merged into out/<name>.pb, decoded to out/<name>.json
A request that offers tools and carries no tool result yet gets a tool call.
A model named "no-such-model" gets a 404.
"""
import base64, gzip, json, pathlib, sys, threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from google.protobuf.json_format import MessageToDict, ParseDict
from opentelemetry.proto.collector.trace.v1.trace_service_pb2 import ExportTraceServiceRequest

OUT = pathlib.Path(__file__).parent / "out"
ARGS = {"city": "Paris"}
LOCK = threading.Lock()  # exporters post concurrently; save rewrites a file


def openai(req):
    tools, msgs = req.get("tools"), req["messages"]
    msg = {"role": "assistant", "content": "Paris."}
    if tools and msgs[-1]["role"] != "tool":
        call = {"name": tools[0]["function"]["name"], "arguments": json.dumps(ARGS)}
        msg = {"role": "assistant", "content": None, "tool_calls": [{"id": "call_1", "type": "function", "function": call}]}
    return {
        "id": "chatcmpl-fake", "object": "chat.completion", "created": 1, "model": req["model"],
        "choices": [{"index": 0, "message": msg, "finish_reason": "tool_calls" if msg["content"] is None else "stop"}],
        "usage": {"prompt_tokens": 1200, "completion_tokens": 40, "total_tokens": 1240,
                  "prompt_tokens_details": {"cached_tokens": 1024},
                  "completion_tokens_details": {"reasoning_tokens": 32}},
    }


def anthropic(req):
    tools, last = req.get("tools"), req["messages"][-1]["content"]
    content = [{"type": "text", "text": "Paris."}]
    if tools and not (isinstance(last, list) and last[0]["type"] == "tool_result"):
        content = [{"type": "tool_use", "id": "toolu_1", "name": tools[0]["name"], "input": ARGS}]
    return {
        "id": "msg_fake", "type": "message", "role": "assistant", "model": req["model"], "content": content,
        "stop_reason": "tool_use" if content[0]["type"] == "tool_use" else "end_turn", "stop_sequence": None,
        # Anthropic's convention: input_tokens excludes both cache counters.
        "usage": {"input_tokens": 20, "cache_read_input_tokens": 1000, "cache_creation_input_tokens": 200, "output_tokens": 5},
    }


GEMINI = {
    "candidates": [{"content": {"role": "model", "parts": [{"text": "Paris."}]}, "finishReason": "STOP", "index": 0}],
    "usageMetadata": {"promptTokenCount": 1200, "cachedContentTokenCount": 1024, "candidatesTokenCount": 8,
                      "thoughtsTokenCount": 32, "totalTokenCount": 1240},
}


def ids(node, convert):
    """OTLP/JSON writes ids as hex; protobuf's JSON mapping wants base64."""
    if isinstance(node, list):
        for item in node:
            ids(item, convert)
    elif isinstance(node, dict):
        for key, value in node.items():
            if key in ("traceId", "spanId", "parentSpanId"):
                node[key] = convert(value)
            else:
                ids(value, convert)


def save(name, body):
    OUT.mkdir(exist_ok=True)
    pb = OUT / f"{name}.pb"
    # Concatenated export requests are one valid request.
    req = ExportTraceServiceRequest.FromString((pb.read_bytes() if pb.exists() else b"") + body)
    # A stack trace carries local paths: the home directory becomes "~".
    doc = json.loads(json.dumps(MessageToDict(req)).replace(str(pathlib.Path.home()), "~"))
    for rs in doc.get("resourceSpans", []):  # and the resource names the machine and the user
        attrs = rs.get("resource", {}).get("attributes", [])
        attrs[:] = [a for a in attrs if not a["key"].startswith(("host.", "process."))]
    pb.write_bytes(ParseDict(doc, ExportTraceServiceRequest()).SerializeToString())
    ids(doc, lambda v: base64.b64decode(v).hex())
    (OUT / f"{name}.json").write_text(json.dumps(doc, indent=2) + "\n")


class Handler(BaseHTTPRequestHandler):
    def body(self):
        if "Content-Length" in self.headers:
            return self.rfile.read(int(self.headers["Content-Length"]))
        body = b""  # Node's OTLP exporter sends chunked
        while size := int(self.rfile.readline(), 16):
            body += self.rfile.read(size)
            self.rfile.readline()
        self.rfile.readline()
        return body

    def do_POST(self):
        body = self.body()
        if self.headers.get("Content-Encoding") == "gzip":
            body = gzip.decompress(body)
        out, ctype, status = b"", "application/x-protobuf", 200  # an empty Export*ServiceResponse
        if self.path.endswith("/v1/traces"):
            if "json" in self.headers.get("Content-Type", ""):
                doc = json.loads(body)
                ids(doc, lambda v: base64.b64encode(bytes.fromhex(v)).decode())
                body = ParseDict(doc, ExportTraceServiceRequest()).SerializeToString()
            with LOCK:
                save(self.path.split("/")[1], body)
        elif b'"no-such-model"' in body:
            error = {"type": "not_found_error", "message": "model: no-such-model"}
            out, ctype, status = json.dumps({"type": "error", "error": error}).encode(), "application/json", 404
        elif self.path.endswith("/chat/completions"):
            out, ctype = json.dumps(openai(json.loads(body))).encode(), "application/json"
        elif self.path.endswith("/messages"):
            out, ctype = json.dumps(anthropic(json.loads(body))).encode(), "application/json"
        elif "GenerateContent" in self.path:  # Gemini; ?alt=sse wants one event
            out, ctype = json.dumps(GEMINI).encode(), "application/json"
            if "alt=sse" in self.path:
                out, ctype = b"data: " + out + b"\n\n", "text/event-stream"
        self.send_response(status)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(out)))
        self.end_headers()
        self.wfile.write(out)

    def log_message(self, fmt, *args):
        print(self.command, self.path, file=sys.stderr)


if __name__ == "__main__":
    ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1]) if len(sys.argv) > 1 else 14331), Handler).serve_forever()
