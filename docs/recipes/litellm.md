# LiteLLM

## What you get

- One trace per call: a `litellm_request` span with the model, input and output tokens, and a `raw_gen_ai_request` span under it with the provider's request and response.
- The cost LiteLLM calculated, shown as reported by the sender.
- Prompts as chat messages, a tool call as a card.

## Configuration

```python
import litellm

litellm.callbacks = ["otel"]
```

```sh
export OTEL_EXPORTER=otlp_http
export OTEL_ENDPOINT=http://127.0.0.1:4318/v1/traces
```

No key or header is needed. `OTEL_ENDPOINT` is the full URL, path included.

## Tested

2026-10-05, `litellm` 1.104.0 as a library (not the proxy), `litellm.completion` with an OpenAI model: `litellm-otel-tool` in `testdata/`.

## Known gaps

- **No cached or reasoning token count on the span.** They are only inside `metadata.usage_object`, as text. LiteLLM's own cost does account for them, which is why spoor stores that cost (`gen_ai.cost.total_cost`) and not its own calculation: in the capture LiteLLM reports $0.0001272, and the span's tokens alone would give $0.000204. The span's cost breakdown shows both.
- **A reported cost is never repriced.** `spoor reprice` leaves these spans alone.
- **No session.** Nothing on the span identifies a conversation.
- **Not captured:** the LiteLLM proxy, streaming, a failed call.
