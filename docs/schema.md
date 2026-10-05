# Schema

The SQLite file is spoor's read API. This page documents it for a person or a coding agent that wants to answer questions such as "why was yesterday's run expensive" or "where did it fail" directly from the data.

Every value below is what the instrumented application sent, or a sum of it. spoor does not summarise or judge.

**Schema version 1.** A database reports its own with `PRAGMA user_version`. The tables are written only by spoor itself (`serve`, `demo`, `migrate`, `reprice`): read them, do not write them.

## Ways to read

| | |
|---|---|
| `spoor export --trace <id>` <br> `spoor export --session <id>` | JSONL, one span per line, complete. `--html` writes one self-contained page instead. |
| `POST /mcp` on the UI port of `spoor serve` | The same data for a coding agent, over the Model Context Protocol: `list_traces`, `get_trace`, `search_spans`. |
| The file | Any SQLite client: `sqlite3 -readonly -json spoor.db "<SQL>"`. spoor runs no SQL for you. |

`export` needs only `SPOOR_SQLITE_PATH` (default `./spoor.db`); the MCP endpoint and `export` read through a **read-only** connection. All three are safe to run while `spoor serve` is ingesting.

In the output of `export` and the MCP tools, a text value that is itself a JSON object or array (`input`, `output`, `metadata`) is embedded as JSON, not as a string. Two columns with the same name collapse into one key: alias them (`SELECT t.name AS trace_name, s.name AS span_name`).

## The model

```
traces 1 ── n spans
```

- A **trace** is one run: one request, one agent task, one turn of a conversation.
- A **span** is one step inside it: a model call, a tool execution, an agent or chain wrapper. Spans form a tree through `parent_span_id`.
- A **session** is not a table: it is every trace sharing a `session_id`, in `started_at` order.
- A **service** is not a table either: it is `traces.service`, the application that sent the trace. It is the only grouping above the trace; there are no projects, accounts or keys.

## Conventions

- **Timestamps** are UTC RFC 3339 text, stored with always nine fractional digits: `2026-10-03T04:38:29.800000000Z`. They compare correctly as strings, including against a prefix: `started_at >= '2026-10-03'`. For arithmetic use `julianday()`: `(julianday(ended_at) - julianday(started_at)) * 86400000` is a duration in milliseconds.
- **NULL means "not reported"**, never zero. A `SUM` over values that are all NULL is NULL.
- **`status`** is `ok`, `error` or `unset`. `unset` is what most SDKs send for a step that worked; it does not mean failure. Only `error` means failure.
- **IDs**: `traces.id` and `spans.id` are the hex ids the application's OpenTelemetry SDK generated. `spans.id` is unique only inside its trace; a span is identified by `(trace_id, id)`.

## Tables

### `traces`

One row per trace, computed by spoor from the spans received so far.

| Column | Meaning |
|---|---|
| `id` | Trace id |
| `service` | The sender's OpenTelemetry `service.name` resource attribute: the first one named among the trace's batches. NULL when the sender set none: spoor never invents a name |
| `session_id` | Conversation or thread id, when the SDK sent one (`session.id`, `gen_ai.conversation.id`, ...). NULL otherwise: spoor never infers a session |
| `name` | The root span's name. Until the root span arrives, the name of the oldest span of the first batch |
| `started_at`, `ended_at` | Earliest start and latest end among its spans |
| `status` | `error` when any of its spans is `error`; otherwise the root span's status |
| `metadata` | JSON object: `resource` (the OTel resource attributes, e.g. `service.name`) and `scope` |
| `created_at` | When spoor first saw the trace. Retention (`SPOOR_RETENTION_DAYS`) counts from here, and deletes the trace with all its spans |

### `spans`

One row per step. Primary key `(trace_id, id)`.

| Column | Meaning |
|---|---|
| `trace_id`, `id` | |
| `parent_span_id` | The parent's `id` in the same trace. NULL for a root. A parent that was never received leaves a dangling id: treat that span as a root too |
| `kind` | `generation` (one model call), `tool` (one tool execution), `agent`, `chain`, `retriever`, `embedding`, `reranker`, or `generic` |
| `name` | The span name the SDK gave, e.g. `anthropic.chat`, `tool.Bash` |
| `started_at`, `ended_at` | |
| `status` | See Conventions. The failing steps of a trace are its spans with `status = 'error'` |
| `model` | Model name as reported by the provider. Usually only on generations |
| `input`, `output` | What went in and came out, complete. See "Input and output" |
| `input_tokens` | **The whole prompt, cache included** |
| `cache_read_tokens` | The part of `input_tokens` served from the provider's prompt cache |
| `cache_write_tokens` | The part of `input_tokens` written to the prompt cache |
| `output_tokens` | The whole completion, reasoning included |
| `reasoning_tokens` | The part of `output_tokens` spent on reasoning |
| `cost_usd` | Cost of this span in US dollars. NULL when the model has no known price: unknown, not free |
| `price_pattern`, `price_fingerprint` | The `model_prices` row that priced `cost_usd`, and a fingerprint of that row's prices and of the matching rule at the time. NULL when spoor did not calculate the cost (the SDK reported it, or the model has no price) |
| `metadata` | JSON object with every attribute the SDK sent that has no column of its own, keyed by its flat dotted name. SDK-specific fields (exit codes, failure kinds) live here |
| `created_at` | When spoor received the span |
| `status_message` | The text the SDK sent with the status: on a failed span, the reason. NULL when none was sent |
| `events` | JSON array of the span's events in the order sent, each `{"name", "time", "attributes"}`. NULL when the span has none. See "Events" |
| `links` | JSON array of `{"trace_id", "span_id", "attributes"}`: spans of other traces this span points at. NULL when none |
| `attributes` | Every attribute the sender put on the span, as sent: a JSON object, **zlib-compressed**, so SQL cannot read it (`spoor export` writes it out as JSON). NULL when the span had none. The columns above are read from it at ingestion; it is kept so a changed reading can be applied to what is stored. It costs about a quarter of the span's text again |

The token contract, in one line: `fresh input = input_tokens - cache_read_tokens - cache_write_tokens`. Token and cost columns are normally set only on `generation` spans, so summing over all spans of a trace is usually right because the others are NULL. Not always: when an SDK repeats a model call's usage on its parent span (an agent or chain wrapper), a plain sum counts it twice. `trace_summary` and spoor's trace and session lists sum that way; the trace page counts such usage once. This is rare, and to check a trace, look for token or cost values on spans that have children.

### `model_prices`

The price table used to compute `cost_usd` at ingestion: `model_pattern` (an anchored regular expression matched against `spans.model`, as sent and with a bracketed tag such as `[1m]`, a date stamp and a `5.5`-for-`5-5` spelling removed; the longest matching pattern wins), `input_price_per_token`, `output_price_per_token`, `cache_read_price_per_token`, `cache_write_price_per_token` (US dollars per token; a NULL cache price is estimated from the input price), `cache_write_1h_price_per_token` (the rate of a cache write kept for one hour, where the provider has two rates; `cache_write_price_per_token` is then the 5-minute rate, and the one `cost_usd` is stored at, because a trace rarely says which lifetime was used), `updated_at`. The binary replaces the rows with its own catalog at every start. Changing it does not reprice spans already stored: `spoor serve` logs how many spans were priced with rows that have since changed, and `spoor reprice` recomputes them.

## View

### `trace_summary`

One row per trace with what its spans add up to. Stable; prefer it to re-deriving the sums.

| Column | Meaning |
|---|---|
| `id`, `service`, `session_id`, `name`, `status`, `started_at`, `ended_at` | As in `traces` |
| `duration_ms` | `ended_at - started_at`, in milliseconds |
| `span_count` | Number of spans |
| `error_span_count` | Number of spans with `status = 'error'` |
| `tool_span_count` | Number of spans with `kind = 'tool'` |
| `input_tokens`, `cache_read_tokens`, `cache_write_tokens`, `output_tokens`, `reasoning_tokens` | Sums over every span of the trace (see the note under `spans` on usage repeated by a parent). NULL when no span reported the value |
| `cost_usd` | Sum of the spans' cost. NULL when no span has a price. When only some spans are priced it is the sum of those: a lower bound |

## Search

### `spans_fts`

An FTS5 full-text index over six columns of `spans`: `name`, `input`, `output`, `metadata`, `status_message`, `events`. Its `rowid` is the span's `rowid`; join on it to get the span. The index holds terms only (the text is read back from `spans`), and triggers on `spans` keep it in step with every insert, delete and update, including retention.

- **Tokens** are runs of letters and digits; everything else separates (`get_stock_price` is three tokens in a row). Case and accents are folded: `cotacao` finds `Cotação`.
- **`search_spans` and the UI's search box** quote each typed word as a prefix phrase and require all of them: `stock_pri petr` becomes `"stock_pri"* "petr"*`. A word matches a whole token or the start of one, never the middle. A `"quoted phrase"` is those whole tokens in that order, and `-term` excludes: `timeout "connection refused" -retry` becomes `"timeout"* "connection refused" NOT "retry"*`. Text with only excluded terms matches nothing. In the UI all terms must be in one span, and the model, kind, tool and "failed" filters describe that same span.
- **In SQL over the file** the whole [FTS5 query syntax](https://www.sqlite.org/fts5.html#full_text_query_syntax) is available: `MATCH 'output: (refused OR timeout)'`, `NEAR(a b, 5)`, and `snippet()` / `highlight()` / `bm25()`.
- It takes disk: in the `spoor demo` database the index is a little under half the size of the `spans` table.
- The index is derived data. `INSERT INTO spans_fts (spans_fts) VALUES ('rebuild')`, run with `sqlite3` while spoor is stopped, re-creates it from `spans`.

## Input and output

`spans.input` and `spans.output` are text. What the text is depends on the span and on the SDK:

**A generation, from an SDK that sends messages** — a JSON array of messages.

```json
input:  [{"role": "system", "content": "..."}, {"role": "user", "content": "..."}]
output: [{"role": "assistant", "content": "...", "finish_reason": "tool_use",
          "tool_calls": [{"id": "toolu_01", "name": "get_weather", "arguments": "{\"city\":\"Paris\"}"}]}]
```

- Every key is optional and absent when empty.
- `tool_calls[].arguments` is a JSON document encoded as a string: `json_extract(c.value, '$.arguments')` gives text to parse again.
- `content` is text. When a turn is not plain text, some SDKs put the provider's own content blocks there as a JSON string, e.g. `[{"type":"tool_use",...}]` or `[{"type":"tool_result",...}]`.
- A later model call of the same conversation usually repeats the earlier messages, so prompts overlap from one generation to the next.

**A tool span** — `input` is usually a JSON object with the tool's arguments; `output` is the tool's result when the SDK sent it.

**Anything else** — whatever the SDK put in `input.value` / `output.value`: JSON or plain text. Guard JSON functions with `json_valid(input)`.

`metadata` keys contain dots, so quote them in a JSON path: `json_extract(metadata, '$."tool.name"')`.

## Events

`spans.events` holds what happened at a point in time inside a span, exactly as the SDK sent it. spoor does not filter or interpret event names.

```json
[{"name": "exception", "time": "2026-10-03T12:00:00.25Z",
  "attributes": {"exception.type": "TimeoutError", "exception.message": "upstream timed out",
                 "exception.stacktrace": "Traceback (most recent call last): ..."}}]
```

- An exception recorded by an OpenTelemetry SDK is the event named `exception`, with the attributes above (`exception.stacktrace` is optional).
- `attributes` is absent when the event has none; its keys contain dots, so quote them in a JSON path.
- Why a span failed: `status_message` first, then its `exception` events. Many SDKs send only one of the two, and some send neither — then look at `output` and `metadata`.

## Example queries

Each of these runs as written with `sqlite3 -readonly -json spoor.db "..."`.

Most expensive traces today:

```sql
SELECT id, service, name, cost_usd, input_tokens, output_tokens, duration_ms
FROM trace_summary
WHERE started_at >= date('now') AND cost_usd IS NOT NULL
ORDER BY cost_usd DESC LIMIT 10
```

Yesterday's failed runs:

```sql
SELECT id, service, name, started_at, error_span_count, span_count
FROM trace_summary
WHERE status = 'error' AND started_at >= date('now', '-1 day') AND started_at < date('now')
ORDER BY started_at
```

The failing steps of recent traces, with what they were called with:

```sql
SELECT trace_id, id, parent_span_id, kind, name, started_at, status_message, input, output, metadata
FROM spans
WHERE status = 'error' AND started_at >= date('now', '-7 days')
ORDER BY started_at LIMIT 50
```

Exceptions recorded in the last week, by type:

```sql
SELECT json_extract(e.value, '$.attributes."exception.type"') AS type, COUNT(*) AS times,
       MAX(json_extract(e.value, '$.attributes."exception.message"')) AS example
FROM spans s, json_each(s.events) e
WHERE json_extract(e.value, '$.name') = 'exception' AND s.started_at >= date('now', '-7 days')
GROUP BY type ORDER BY times DESC
```

Tool calls per trace, by tool:

```sql
SELECT trace_id, name AS tool, COUNT(*) AS calls, SUM(status = 'error') AS failed
FROM spans
WHERE kind = 'tool'
GROUP BY trace_id, name
ORDER BY calls DESC LIMIT 50
```

Tool calls the model asked for, read from generation outputs (for SDKs that do not emit tool spans):

```sql
SELECT s.trace_id, json_extract(c.value, '$.name') AS tool, COUNT(*) AS calls
FROM spans s, json_each(s.output) m, json_each(m.value, '$.tool_calls') c
WHERE s.kind = 'generation' AND json_valid(s.output)
GROUP BY s.trace_id, tool
ORDER BY calls DESC LIMIT 50
```

Tokens by bucket per session:

```sql
SELECT session_id, COUNT(*) AS traces,
       SUM(cache_read_tokens) AS cache_read,
       SUM(cache_write_tokens) AS cache_write,
       SUM(input_tokens) - COALESCE(SUM(cache_read_tokens), 0) - COALESCE(SUM(cache_write_tokens), 0) AS fresh_input,
       SUM(output_tokens) AS output,
       SUM(cost_usd) AS cost_usd
FROM trace_summary
WHERE session_id IS NOT NULL
GROUP BY session_id
ORDER BY MAX(started_at) DESC LIMIT 20
```

Traces and cost per service (a NULL service is a sender that named none):

```sql
SELECT service, COUNT(*) AS traces, SUM(cost_usd) AS cost_usd, MAX(started_at) AS last_seen
FROM trace_summary
GROUP BY service
ORDER BY traces DESC
```

Where the money went, by model:

```sql
SELECT model, COUNT(*) AS calls, SUM(input_tokens) AS input_tokens,
       SUM(output_tokens) AS output_tokens, SUM(cost_usd) AS cost_usd,
       SUM(cost_usd IS NULL) AS calls_without_price
FROM spans
WHERE kind = 'generation'
GROUP BY model
ORDER BY cost_usd DESC
```

The largest prompts of one trace (put a real id in place of `TRACE_ID`):

```sql
SELECT id, name, model, input_tokens, cache_read_tokens, output_tokens, cost_usd, length(input) AS input_chars
FROM spans
WHERE trace_id = 'TRACE_ID' AND kind = 'generation'
ORDER BY input_tokens DESC LIMIT 5
```

The slowest steps of a kind, to judge whether one is unusual:

```sql
SELECT trace_id, id, name,
       CAST(ROUND((julianday(ended_at) - julianday(started_at)) * 86400000) AS INTEGER) AS duration_ms
FROM spans
WHERE kind = 'tool'
ORDER BY duration_ms DESC LIMIT 10
```

Spans mentioning a word, through the full-text index (see "Search" above):

```sql
SELECT s.trace_id, s.id, s.name, s.kind, s.status, snippet(spans_fts, -1, '**', '**', '…', 24) AS snippet
FROM spans_fts JOIN spans s ON s.rowid = spans_fts.rowid
WHERE spans_fts MATCH '"timeout"*'
ORDER BY s.started_at DESC LIMIT 20
```

Spans containing an arbitrary substring (a full scan of the table; ASCII-only case folding):

```sql
SELECT trace_id, id, name, kind, status
FROM spans
WHERE instr(lower(input), 'timeout') OR instr(lower(output), 'timeout') OR instr(lower(metadata), 'timeout')
ORDER BY started_at DESC LIMIT 20
```

## Limits worth knowing

- **Search matches words, not substrings.** `Timeout` does not find `ConnectionTimeout`; use the `instr` scan above for that. Text is indexed as stored: a non-ASCII character an SDK sent as a `\u00e7` escape inside a JSON string (common in `metadata`) is not found by typing the character.
- **A session exists only if the SDK sent an id.** Two traces of the same conversation without one cannot be linked.
- **Cost is priced at ingestion** from `model_prices` as it was then; `spoor reprice` applies the current table.
- **One statement per query.** Do not rely on what a string with several statements returns.
