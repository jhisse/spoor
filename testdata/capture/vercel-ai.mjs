// Vercel AI SDK 7: telemetry comes from @ai-sdk/otel. Versions are pinned in package.json.
import { createOpenAI } from '@ai-sdk/openai';
import { OpenTelemetry } from '@ai-sdk/otel';
import { OTLPTraceExporter } from '@opentelemetry/exporter-trace-otlp-proto';
import { NodeTracerProvider, SimpleSpanProcessor } from '@opentelemetry/sdk-trace-node';
import { generateText, isStepCount, registerTelemetry, tool } from 'ai';
import { z } from 'zod';

const provider = new NodeTracerProvider({ spanProcessors: [new SimpleSpanProcessor(new OTLPTraceExporter())] });
provider.register();
registerTelemetry(new OpenTelemetry());

const withTool = process.argv[2] === 'tool';
const getWeather = tool({
  description: 'Weather in a city',
  inputSchema: z.object({ city: z.string() }),
  execute: async () => '18 C, clear',
});
await generateText({
  model: createOpenAI().chat('gpt-4o-mini'),
  system: 'Answer in one word.',
  prompt: withTool ? 'Weather in Paris?' : 'Capital of France?',
  tools: withTool ? { get_weather: getWeather } : undefined,
  stopWhen: isStepCount(3),
});
await provider.shutdown();
