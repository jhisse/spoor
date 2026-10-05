// Package store defines the Store interface and the domain types. The rest
// of spoor talks only to Store, never to SQL, so storage is an adapter.
package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

var ErrNotFound = errors.New("not found")

// Deref reads an optional field: the zero value when it is nil.
func Deref[T any](p *T) (v T) {
	if p != nil {
		v = *p
	}
	return v
}

type Status string

const (
	StatusUnset Status = "unset"
	StatusOK    Status = "ok"
	StatusError Status = "error"
)

type SpanKind string

const (
	SpanKindGeneric    SpanKind = "generic"
	SpanKindGeneration SpanKind = "generation"
	SpanKindAgent      SpanKind = "agent"
	SpanKindChain      SpanKind = "chain"
	SpanKindTool       SpanKind = "tool"
	SpanKindRetriever  SpanKind = "retriever"
	SpanKindEmbedding  SpanKind = "embedding"
	SpanKindReranker   SpanKind = "reranker"
)

// Cursor is a keyset pagination position: the (started_at, id) pair of the
// last row of the previous page. Never an OFFSET, which repeats or skips
// rows when data changes between page fetches.
type Cursor struct {
	StartedAt time.Time
	ID        string
}

// EncodeCursor/DecodeCursor round-trip a Cursor through a URL query
// parameter, opaque to whoever carries it.
func EncodeCursor(c Cursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func DecodeCursor(s string) (c Cursor, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err == nil {
		err = json.Unmarshal(raw, &c)
	}
	if err != nil {
		return Cursor{}, fmt.Errorf("decoding cursor: %w", err)
	}
	return c, nil
}

// TraceQuery is QueryTraces' input. Every filter is optional.
type TraceQuery struct {
	Service  string     // "" = every service
	From, To *time.Time // nil = no bound
	Status   *Status    // nil = no filter
	Span     SpanFilter // only traces with a span it describes
	Cursor   *Cursor    // nil = first page
	Limit    int        // caller should clamp to [1,200]; Store clamps too
	// One range per Measure: Min <= measure < Max, 0 = no bound. An unknown
	// measure matches no bound.
	Ranges map[Measure]Range
}

type Range struct{ Min, Max float64 }

// Measure is a per-trace magnitude: USD or input plus output tokens summed
// over spans (unknown if none reports it), or seconds.
type Measure string

const MeasureCost, MeasureDuration, MeasureTokens Measure = "cost", "duration", "tokens"

// SpanFilter describes one span: every set field must hold on the same
// span. The zero value describes any span.
type SpanFilter struct {
	Text   string   // search text, as ParseSearch reads it
	Kind   SpanKind // "" = any
	Name   string   // the exact span name; "" = any
	Model  string   // "" = any
	Failed bool     // status error
}

type SearchTerm struct {
	Text   string
	Phrase bool // quoted: these whole words, in this order. Otherwise Text is the start of a word
	Not    bool // -term: the span must not hold it
}

// ParseSearch reads what a person types into a search box: words, "quoted
// phrases" and -excluded terms. A term without a letter or digit is dropped.
func ParseSearch(text string) (terms []SearchTerm) {
	space := func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }
	for text = strings.TrimFunc(text, space); text != ""; text = strings.TrimFunc(text, space) {
		var t SearchTerm
		if t.Not = text[0] == '-'; t.Not {
			text = text[1:]
		}
		end := strings.IndexFunc(text, func(r rune) bool { return space(r) || r == '"' })
		if strings.HasPrefix(text, `"`) {
			t.Phrase, text = true, text[1:]
			end = strings.IndexByte(text, '"')
		}
		if end < 0 {
			end = len(text)
		}
		if t.Text, text = text[:end], text[end:]; t.Phrase {
			text = strings.TrimPrefix(text, `"`)
		}
		if strings.ContainsFunc(t.Text, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
			terms = append(terms, t)
		}
	}
	return terms
}

// HeatCell counts the traces in time bucket X whose measure reaches Y of
// the heat map's bounds. TraceID is one of them: the trace when Count is 1.
type HeatCell struct {
	X, Y    int
	Count   int64
	TraceID string
}

// TraceSummary is a Trace plus aggregates over its spans: list-view
// numbers, not properties of the trace itself.
type TraceSummary struct {
	Trace

	// TotalCostUSD is nil when no span in the trace has a cost — distinct
	// from a real total of zero, so the list shows "–", not "$0.0000".
	TotalCostUSD *float64

	// Models is the distinct set of models of the trace's generation spans,
	// the one that cost most first: a model with no cost comes last, ties
	// go to the one with more calls, then to the name.
	Models []string

	// TotalInputTokens includes the two cache buckets below (see Span);
	// those are 0 when no span reported them.
	TotalInputTokens      int64
	TotalOutputTokens     int64
	TotalCacheReadTokens  int64
	TotalCacheWriteTokens int64

	// Rootless: the trace has spans and every one names a parent, so its
	// root has not arrived (the sender is still working, or lost it).
	Rootless bool

	// CacheUnknown counts the spans whose cost spoor calculated without any
	// cache count from the sender (Span.CacheUnknown).
	CacheUnknown int64

	// SpanCount and ErrorSpans count the trace's spans, and those with
	// status error.
	SpanCount, ErrorSpans int64
}

// TraceBucket is one bar of the traces-over-time histogram: how many
// traces started in [Start, End), by status.
type TraceBucket struct {
	Start, End       time.Time
	OK, Error, Unset int64
}

// SessionSummary aggregates the traces sharing one session_id. Sessions are
// not stored: they live exactly as long as their traces.
type SessionSummary struct {
	ID                    string
	FirstTraceID          string // the earliest turn: what the session is about is read from it
	StartedAt             time.Time
	EndedAt               time.Time
	TraceCount            int64
	Status                Status
	TotalCostUSD          *float64
	TotalInputTokens      int64
	TotalOutputTokens     int64
	TotalCacheReadTokens  int64
	TotalCacheWriteTokens int64
	CacheUnknown          int64 // as TraceSummary.CacheUnknown
}

type Trace struct {
	ID string
	// Service is the sender's service.name resource attribute; nil when it
	// set none.
	Service   *string
	SessionID *string
	Name      string
	StartedAt time.Time
	EndedAt   time.Time
	Status    Status
	Metadata  json.RawMessage
	CreatedAt time.Time
}

// TraceMerge is one batch's view of a trace and whether the batch carried
// its root span: the two arguments of MergeTrace, for IngestBatch.
type TraceMerge struct {
	Trace
	HasRoot bool
}

type ModelPrice struct {
	ModelPattern        string
	InputPricePerToken  float64
	OutputPricePerToken float64
	// Nil means the price sheet has no cache rate for this model; cost
	// calculation then estimates one from the input price.
	CacheReadPricePerToken  *float64
	CacheWritePricePerToken *float64
	// CacheWrite1hPricePerToken is the rate of a cache write kept for one
	// hour, where the provider has one; CacheWritePricePerToken is then the
	// 5-minute rate. Costs are stored at the 5-minute rate.
	CacheWrite1hPricePerToken *float64
	UpdatedAt                 time.Time
}

type Span struct {
	TraceID      string
	ID           string
	ParentSpanID *string
	Kind         SpanKind
	Name         string
	StartedAt    time.Time
	EndedAt      time.Time
	Status       Status
	Model        *string
	Input        *string
	Output       *string
	// InputTokens is the whole prompt, cache included. CacheReadTokens and
	// CacheWriteTokens are the parts of it served from / written to the
	// provider's prompt cache; fresh input is what remains. ReasoningTokens
	// is the part of OutputTokens spent on reasoning. Nil means the SDK
	// didn't report it, never zero.
	InputTokens      *int64
	OutputTokens     *int64
	CacheReadTokens  *int64
	CacheWriteTokens *int64
	ReasoningTokens  *int64
	CostUSD          *float64
	// PricePattern is the model_prices row that priced CostUSD and
	// PriceFingerprint that row's ModelPrice.Fingerprint at the time. Nil
	// when spoor did not calculate the cost.
	PricePattern     *string
	PriceFingerprint *string
	Metadata         json.RawMessage
	CreatedAt        time.Time
	// StatusMessage is the text sent with the status (why it failed); Events
	// a JSON array of SpanEvent; Links one of {trace_id, span_id, attributes}.
	StatusMessage *string
	Events        json.RawMessage
	Links         json.RawMessage
	// Attributes is every attribute the sender put on the span, as sent, a
	// JSON object; nil when there were none. The columns above are read from
	// it at ingestion. Only the adapter knows how it is stored.
	Attributes json.RawMessage
}

// SpanEvent is one element of Span.Events: something that happened at a
// point in time inside the span, such as a recorded exception.
type SpanEvent struct {
	Name       string          `json:"name"`
	Time       time.Time       `json:"time"`
	Attributes json.RawMessage `json:"attributes,omitempty"`
}

// SpanQuery selects spans across traces; a zero field does not filter.
type SpanQuery struct {
	Service   string // spans of that service's traces
	SessionID string // spans of that session's traces
	TraceIDs  []string
	Kind      SpanKind
	From, To  *time.Time // inclusive bounds on the span's own start
	Bodies    bool       // false leaves Input/Output nil: they dominate a row's size
	First     bool       // only each trace's earliest span of its kind
	// Usage returns only what a token chart reads: ids, parent, kind, model,
	// start, end, token counts, cost and price pattern; every other field is
	// zero. It is answered from an index, without reading a span's row.
	Usage bool
}

// SpanHit is one span found by MatchingSpans. Snippet is a short stretch of
// the matching text, never the whole body, with each matched word between
// HitStart and HitEnd (control characters, which stored JSON never holds).
type SpanHit struct {
	TraceID, SpanID, SpanName string
	Kind                      SpanKind
	Status                    Status
	Snippet                   string
}

const HitStart, HitEnd = "\x02", "\x03"

// BlindSpots is what spoor stores but cannot read, counted so the page of
// that name can say it. Which models have no price is decided in Go over
// ModelSpans (MatchModelPrice), like every other price match.
type BlindSpots struct {
	Traces, Spans, Generations int64
	Rootless                   int64 // traces whose every span names a parent: the root never arrived
	Orphans                    int64 // spans whose parent is not in the trace; shown as roots, marked "no parent"
	NoInputTokens              int64 // generation spans with no input token count
	NoModel                    int64 // generation spans with no model
	CacheUnknown               int64 // generation spans priced without cache counts (Span.CacheUnknown)
	NoMessages                 int64 // generation spans whose stored prompt is not a list of messages
	ModelSpans                 []ModelCount
}

// CostWindow is what the traces of a period cost: USD sums every span
// with a cost, Uncosted is the traces no span of which has one.
type CostWindow struct {
	USD              float64
	Traces, Uncosted int64
}

// ModelCount is the generation spans of one model.
type ModelCount struct {
	Model string
	Spans int64
}

// Store is the single port to data. It grows one method at a time, only
// when something calls it.
type Store interface {
	// ListServices returns the distinct services traces were stored under,
	// sorted. Traces without one are not represented.
	ListServices(ctx context.Context) ([]string, error)

	// ListModelPrices returns every price row: the caller matches in Go
	// (MatchModelPrice), so the rule is the same on every backend.
	ListModelPrices(ctx context.Context) ([]ModelPrice, error)

	// ListGenerationModels returns the distinct models seen on generation
	// spans, sorted.
	ListGenerationModels(ctx context.Context) ([]string, error)

	// BlindSpots counts what the stored data does not say.
	BlindSpots(ctx context.Context) (BlindSpots, error)

	// CostSince sums the stored cost of the traces q describes that started
	// at or after since, and counts them and those with no cost at all.
	CostSince(ctx context.Context, q TraceQuery, since time.Time) (CostWindow, error)

	Ping(ctx context.Context) error

	// MergeTrace folds one batch's view of a trace into the stored row in
	// one atomic statement, so concurrent batches of a trace cannot
	// overwrite each other: earliest start, latest end, error status
	// sticky. hasRoot says the batch carried the root span, whose name,
	// status and session id replace what is stored; without it they only
	// fill what is still empty. The service is the first one any batch named.
	MergeTrace(ctx context.Context, t Trace, hasRoot bool) error

	// InsertSpan upserts by (trace_id, id), first write wins: a retried span
	// is neither duplicated nor overwritten by a partial resend.
	InsertSpan(ctx context.Context, s Span) error

	// IngestBatch stores one OTLP request, all or nothing: each span as
	// InsertSpan would, each trace as MergeTrace would. A failure leaves
	// nothing written, so the sender's retry of the whole request is safe.
	// It is the only production writer of spans and traces; the two
	// methods above are its parts, and seed tests.
	IngestBatch(ctx context.Context, traces []TraceMerge, spans []Span) error

	// SpanPricings groups the spans spoor priced by model, price row and
	// fingerprint: what StalePricings compares with today's table.
	SpanPricings(ctx context.Context) ([]SpanPricing, error)

	// UpdateSpanUsage rewrites the token and cost columns of each given
	// span, all or nothing. It is how `spoor reprice` applies a changed price
	// table to stored spans; ingestion never calls it.
	UpdateSpanUsage(ctx context.Context, spans []Span) error

	// GetTraceByID returns a trace and its spans, oldest first, or
	// ErrNotFound.
	GetTraceByID(ctx context.Context, traceID string) (Trace, []Span, error)

	// GetTraceSummary returns the trace as QueryTraces lists it, or
	// ErrNotFound.
	GetTraceSummary(ctx context.Context, traceID string) (TraceSummary, error)

	// QuerySpans returns the spans matching q, oldest first.
	QuerySpans(ctx context.Context, q SpanQuery) ([]Span, error)

	// ListSpanNames returns the distinct names of the spans of a kind, sorted.
	ListSpanNames(ctx context.Context, kind SpanKind) ([]string, error)

	// MatchingSpans returns the spans f describes in the given traces, each
	// trace's oldest first. With f.Text, a span matches when its name, input,
	// output, metadata, status message or events hold every word (whole or
	// as the start of a word, in any order, ignoring case and accents) and
	// every quoted phrase, and none of the -excluded terms; the first hit of
	// each trace then carries a Snippet. Text with nothing to look for
	// matches no span.
	MatchingSpans(ctx context.Context, f SpanFilter, traceIDs []string) ([]SpanHit, error)

	// ComparableSpans measures the newest limit spans started since then with
	// like's kind, name and model (a nil model only matches nil).
	ComparableSpans(ctx context.Context, like Span, since time.Time, limit int) (durations []time.Duration, inputTokens []int64, err error)

	// QueryTraces returns one page of traces, newest first, plus the
	// cursor for the next page (nil if there isn't one). Filters apply
	// inside the query, never in memory after paginating: that would
	// return an incomplete page.
	QueryTraces(ctx context.Context, q TraceQuery) (traces []TraceSummary, next *Cursor, err error)

	// TraceHistogram counts the traces matching q's filters (Cursor and
	// Limit are ignored: it describes the whole filtered set, not one page)
	// in at most maxBuckets equal, whole-minute buckets spanning the first
	// to the last matching trace. Empty buckets are included; no matching
	// trace means no buckets.
	TraceHistogram(ctx context.Context, q TraceQuery, maxBuckets int) ([]TraceBucket, error)

	// TraceHeatmap counts the same set per TraceHistogram time bucket and per bucket of
	// m, cut at the ascending bounds. Traces whose measure is unknown are left out.
	TraceHeatmap(ctx context.Context, q TraceQuery, m Measure, start time.Time, width time.Duration, bounds []float64) ([]HeatCell, error)

	// AdjacentTraces returns the trace immediately newer (prev) and older
	// (next) than cur in QueryTraces' unfiltered order.
	// Either is nil, not an error, at the newest/oldest trace.
	AdjacentTraces(ctx context.Context, cur Cursor) (prev, next *Trace, err error)

	// QuerySessions lists the sessions, newest first, paginated by their
	// most recent trace's end time and id so live ingestion cannot repeat
	// rows. A service narrows it to that service's traces; "" is all.
	QuerySessions(ctx context.Context, service string, cursor *Cursor, limit int) (sessions []SessionSummary, next *Cursor, err error)

	// GetSession returns a session's aggregate and its traces in
	// chronological order, or ErrNotFound when no trace has that session id.
	GetSession(ctx context.Context, sessionID string) (SessionSummary, []TraceSummary, error)

	// SweepExpired deletes every trace first stored before cutoff, with all
	// its spans whenever they arrived, and any span left without a trace.
	// The counts are for the caller's log line.
	SweepExpired(ctx context.Context, cutoff time.Time) (deletedTraces, deletedSpans int64, err error)

	Close() error
}
