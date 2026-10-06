# GitHub Copilot Chat in VS Code

## What you get

- **One trace per turn**, named `invoke_agent GitHub Copilot Chat`, with what you typed as its input and the answer as its output. Under it, as siblings, one `chat <model>` per model call and one `execute_tool <tool>` per tool call (`read_file`, `run_in_terminal`, `apply_patch`, ...), each with its arguments and result. A tool that failed is an error span with the message.
- **Model, input, cache read, output and reasoning tokens** on every model call, with the messages when content capture is on.
- **Cost, calculated by spoor** per bucket from its price table. Copilot puts no cost on spans. It is an API-price figure: on a Copilot plan it is not what you pay.
- **Sessions.** Every span carries `gen_ai.conversation.id`, so the turns of one chat group on the sessions page.
- **Background calls** (the title of a chat, embeddings for tool search) are there as traces of their own.

## Configuration

In VS Code's user `settings.json`, then reload the window. These settings belong to the VS Code extension; the other editors' Copilot Chat was not tested.

```json
{
  "github.copilot.chat.otel.enabled": true,
  "github.copilot.chat.otel.exporterType": "otlp-http",
  "github.copilot.chat.otel.otlpEndpoint": "http://127.0.0.1:4318",
  "github.copilot.chat.otel.captureContent": true
}
```

No key or header is needed. The endpoint is spoor's ingest address without a path; Copilot adds `/v1/traces`, and sends metrics and logs too, which spoor accepts and drops. The default wire protocol is `http/json`; `github.copilot.chat.otel.protocol` can set `http/protobuf`, and `grpc` does not work.

`captureContent` puts prompts, answers, tool arguments and tool results on the spans, readable by anyone who can open spoor. Without it the spans still carry the model, the tokens and the cost.

The environment variables `COPILOT_OTEL_ENABLED`, `COPILOT_OTEL_ENDPOINT` and `OTEL_EXPORTER_OTLP_ENDPOINT` override the settings; enterprise-managed OTel settings replace them.

## Tested

2026-10-05, VS Code 1.139.1 with the bundled Copilot Chat 0.67.0, agent mode: 35 turns of a real session with `gpt-5.6-luna`, read from the stored rows. There is no capture in `testdata/`: the traces hold the operator's files. The attribute names are the OTel GenAI conventions, which spoor already reads; nothing was added for Copilot.

Settings read from the extension's manifest on the same day (`github.copilot.chat.otel.*`).

## Known gaps

- **A model call's messages are only what was added since the previous call** (the Responses API carries the rest by reference). The Read view shows that delta; the token counts cover the whole prompt.
- **Reasoning is `[encrypted]`.** `copilot_chat.reasoning_content` holds that word; the reasoning token count is on the span.
- **`read_file`'s result is the editor's rendering tree**, not the file text. The text is in the next model call's `tool` message.
- **The turn's root span names your repository, branch, commit and GitHub organisation** (`copilot_chat.repo.remote_url`, `github.copilot.github.org`, ...) and tool spans carry absolute paths. spoor stores them as sent.
- **Background calls have no agent span** and status unset. The embeddings spans carry no token count and no cost.
- **Not captured:** inline chat, edits mode, a model from another provider (BYOK), a failed model call.
