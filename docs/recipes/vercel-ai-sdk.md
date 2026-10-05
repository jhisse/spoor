# Vercel AI SDK

## What you get

- One trace per `generateText` call: an `invoke_agent` root, one `step N` per step, and under each step its model call (`chat <model>`) and tool calls (`execute_tool <tool>`).
- Model, tokens and cost on model calls, with cache read tokens.
- The system instructions, prompts and completions as chat messages; a tool span's arguments and result.

## Configuration

In AI SDK 7, OpenTelemetry is in `@ai-sdk/otel`; once registered, every call is traced.

```js
import { OpenTelemetry } from '@ai-sdk/otel';
import { OTLPTraceExporter } from '@opentelemetry/exporter-trace-otlp-proto';
import { BatchSpanProcessor, NodeTracerProvider } from '@opentelemetry/sdk-trace-node';
import { registerTelemetry } from 'ai';

const exporter = new OTLPTraceExporter({ url: 'http://127.0.0.1:4318/v1/traces' });
new NodeTracerProvider({ spanProcessors: [new BatchSpanProcessor(exporter)] }).register();
registerTelemetry(new OpenTelemetry());
```

No key or header is needed. Set `OTEL_SERVICE_NAME`, or spoor lists the traces under `unknown_service:node`.

## Tested

2026-10-05, `ai` 7.0.127, `@ai-sdk/otel` 1.0.127, `@ai-sdk/openai` 4.0.83 (chat completions), `@opentelemetry/sdk-trace-node` 2.11.0: `vercel-ai-tool` in `testdata/`. Documentation read: the telemetry guide shipped in the `ai` package.

## Known gaps

- **The root span repeats its steps' usage.** `invoke_agent` carries the total tokens of the calls under it. spoor keeps that total in the span's metadata and does not store it as tokens or price it, so nothing is counted twice.
- **No reasoning token count** on the spans.
- **No session.** The SDK sends no conversation id.
- **Not captured:** `streamText`, the Responses API, AI SDK 6 and earlier (`experimental_telemetry`, with `ai.*` attribute names).
