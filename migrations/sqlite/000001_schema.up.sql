-- Every timestamp is UTC text with nine fractional digits
-- (2026-10-03T04:00:00.000000000Z): fixed width, so text order is time order.

-- One row per trace, assembled by the receiver from the spans sharing a
-- trace id. service is the sender's service.name resource attribute, NULL
-- when it set none: unknown stays unknown.
CREATE TABLE traces (
    id         TEXT PRIMARY KEY,
    service    TEXT,
    session_id TEXT,
    name       TEXT NOT NULL,
    status     TEXT NOT NULL,
    started_at TIMESTAMP NOT NULL,
    ended_at   TIMESTAMP NOT NULL,
    metadata   TEXT,
    created_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_traces_started ON traces (started_at, id);
CREATE INDEX idx_traces_service_started ON traces (service, started_at, id);
CREATE INDEX idx_traces_session_started ON traces (session_id, started_at);

-- PK is (trace_id, id), not id alone: OTLP only guarantees span_id
-- uniqueness within its own trace. trace_id has no foreign key: a span is
-- stored before its trace row, in the same transaction.
--
-- input_tokens is the WHOLE prompt; cache_read_tokens and cache_write_tokens
-- say how much of it was served from or written to the provider's prompt
-- cache. reasoning_tokens is the part of output_tokens spent on reasoning.
-- price_pattern is the model_prices row that priced cost_usd and
-- price_fingerprint that row's prices and matching rule at the time; both
-- NULL when spoor did not calculate the cost.
--
-- attributes is every attribute the sender put on the span, as sent: a JSON
-- object, zlib-compressed (about a quarter of its size), NULL when there were
-- none. The columns above are read from it at ingestion; keeping it means a
-- changed reading can be applied to what is already stored.
CREATE TABLE spans (
    trace_id           TEXT NOT NULL,
    id                 TEXT NOT NULL,
    parent_span_id     TEXT,
    kind               TEXT NOT NULL,
    name               TEXT NOT NULL,
    status             TEXT NOT NULL,
    status_message     TEXT,
    started_at         TIMESTAMP NOT NULL,
    ended_at           TIMESTAMP NOT NULL,
    model              TEXT,
    input              TEXT,
    output             TEXT,
    input_tokens       INTEGER,
    output_tokens      INTEGER,
    cache_read_tokens  INTEGER,
    cache_write_tokens INTEGER,
    reasoning_tokens   INTEGER,
    cost_usd           REAL,
    price_pattern      TEXT,
    price_fingerprint  TEXT,
    metadata           TEXT,
    events             TEXT,
    links              TEXT,
    attributes         BLOB,
    created_at         TIMESTAMP NOT NULL,
    PRIMARY KEY (trace_id, id)
);
CREATE INDEX idx_spans_started ON spans (started_at);
CREATE INDEX idx_spans_kind_model ON spans (kind, model);
-- The list's tool filter and its menu of tool names read only this.
CREATE INDEX idx_spans_kind_name ON spans (kind, name, trace_id);
-- The trace list, the sessions list and the cost heat map all aggregate
-- spans per trace, and the list draws each trace's context per model call;
-- without this they read every span row, bodies included.
CREATE INDEX idx_spans_trace_usage ON spans (
    trace_id, cost_usd, input_tokens, output_tokens,
    cache_read_tokens, cache_write_tokens, kind, model, price_pattern,
    started_at, ended_at, id, parent_span_id
);

CREATE TABLE model_prices (
    model_pattern                  TEXT PRIMARY KEY,
    input_price_per_token          REAL NOT NULL,
    output_price_per_token         REAL NOT NULL,
    cache_read_price_per_token     REAL,
    cache_write_price_per_token    REAL,
    cache_write_1h_price_per_token REAL,
    updated_at                     TIMESTAMP NOT NULL
);

-- Full-text index over a span's text (docs/schema.md, "Search"). External
-- content: it holds terms only and reads the text back from spans by
-- rowid. unicode61 splits on non-alphanumerics, folds case and accents.
CREATE VIRTUAL TABLE spans_fts USING fts5(
    name, input, output, metadata, status_message, events,
    content='spans', tokenize='unicode61 remove_diacritics 2'
);
-- A span ignored by ON CONFLICT DO NOTHING fires no trigger.
CREATE TRIGGER spans_fts_insert AFTER INSERT ON spans BEGIN
    INSERT INTO spans_fts (rowid, name, input, output, metadata, status_message, events)
    VALUES (new.rowid, new.name, new.input, new.output, new.metadata, new.status_message, new.events);
END;
CREATE TRIGGER spans_fts_delete AFTER DELETE ON spans BEGIN
    INSERT INTO spans_fts (spans_fts, rowid, name, input, output, metadata, status_message, events)
    VALUES ('delete', old.rowid, old.name, old.input, old.output, old.metadata, old.status_message, old.events);
END;
CREATE TRIGGER spans_fts_update AFTER UPDATE OF name, input, output, metadata, status_message, events ON spans BEGIN
    INSERT INTO spans_fts (spans_fts, rowid, name, input, output, metadata, status_message, events)
    VALUES ('delete', old.rowid, old.name, old.input, old.output, old.metadata, old.status_message, old.events);
    INSERT INTO spans_fts (rowid, name, input, output, metadata, status_message, events)
    VALUES (new.rowid, new.name, new.input, new.output, new.metadata, new.status_message, new.events);
END;

-- Part of the documented read API (docs/schema.md): one row per trace with what
-- its spans add up to. A SUM is NULL, not 0, when no span reported the
-- value: unknown stays unknown.
CREATE VIEW trace_summary AS
SELECT t.id, t.service, t.session_id, t.name, t.status, t.started_at, t.ended_at,
       CAST(ROUND((julianday(t.ended_at) - julianday(t.started_at)) * 86400000) AS INTEGER) AS duration_ms,
       COUNT(s.id) AS span_count,
       COALESCE(SUM(s.status = 'error'), 0) AS error_span_count,
       COALESCE(SUM(s.kind = 'tool'), 0) AS tool_span_count,
       SUM(s.input_tokens) AS input_tokens,
       SUM(s.cache_read_tokens) AS cache_read_tokens,
       SUM(s.cache_write_tokens) AS cache_write_tokens,
       SUM(s.output_tokens) AS output_tokens,
       SUM(s.reasoning_tokens) AS reasoning_tokens,
       SUM(s.cost_usd) AS cost_usd
FROM traces t
LEFT JOIN spans s ON s.trace_id = t.id
GROUP BY t.id;
