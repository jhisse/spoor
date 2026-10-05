package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// traceRow is a trace as the MCP tools show it. A token total of 0 is shown
// as null: the Store cannot tell a sum of zeros from nothing reported.
type traceRow struct {
	ID               string       `json:"id"`
	Service          *string      `json:"service"`
	SessionID        *string      `json:"session_id"`
	Name             string       `json:"name"`
	Status           store.Status `json:"status"`
	StartedAt        time.Time    `json:"started_at"`
	EndedAt          time.Time    `json:"ended_at"`
	DurationMS       int64        `json:"duration_ms"`
	SpanCount        int64        `json:"span_count"`
	ErrorSpanCount   int64        `json:"error_span_count"`
	InputTokens      *int64       `json:"input_tokens"`
	CacheReadTokens  *int64       `json:"cache_read_tokens"`
	CacheWriteTokens *int64       `json:"cache_write_tokens"`
	OutputTokens     *int64       `json:"output_tokens"`
	CostUSD          *float64     `json:"cost_usd"`
	Models           []string     `json:"models,omitempty"`
}

func newTraceRow(t store.TraceSummary) traceRow {
	known := func(n int64) *int64 {
		if n == 0 {
			return nil
		}
		return &n
	}
	return traceRow{ID: t.ID, Service: t.Service, SessionID: t.SessionID, Name: t.Name, Status: t.Status,
		StartedAt: t.StartedAt, EndedAt: t.EndedAt, DurationMS: t.EndedAt.Sub(t.StartedAt).Milliseconds(),
		SpanCount: t.SpanCount, ErrorSpanCount: t.ErrorSpans,
		InputTokens: known(t.TotalInputTokens), CacheReadTokens: known(t.TotalCacheReadTokens),
		CacheWriteTokens: known(t.TotalCacheWriteTokens), OutputTokens: known(t.TotalOutputTokens),
		CostUSD: t.TotalCostUSD, Models: t.Models}
}

// spanRow is a span as the MCP tools and `spoor export` show it. Its text
// columns that hold JSON are embedded as JSON; a *Cut field says a value was
// cut and how long it was.
type spanRow struct {
	TraceID          string         `json:"trace_id"`
	ID               string         `json:"id"`
	ParentSpanID     *string        `json:"parent_span_id"`
	Kind             store.SpanKind `json:"kind"`
	Name             string         `json:"name"`
	StartedAt        time.Time      `json:"started_at"`
	EndedAt          time.Time      `json:"ended_at"`
	Status           store.Status   `json:"status"`
	StatusMessage    *string        `json:"status_message"`
	Model            *string        `json:"model"`
	InputTokens      *int64         `json:"input_tokens"`
	OutputTokens     *int64         `json:"output_tokens"`
	CacheReadTokens  *int64         `json:"cache_read_tokens"`
	CacheWriteTokens *int64         `json:"cache_write_tokens"`
	ReasoningTokens  *int64         `json:"reasoning_tokens"`
	CostUSD          *float64       `json:"cost_usd"`
	PricePattern     *string        `json:"price_pattern"`
	PriceFingerprint *string        `json:"price_fingerprint"`
	Input            any            `json:"input"`
	InputCut         *int           `json:"input_truncated_from_chars,omitempty"`
	Output           any            `json:"output"`
	OutputCut        *int           `json:"output_truncated_from_chars,omitempty"`
	Metadata         any            `json:"metadata"`
	MetadataCut      *int           `json:"metadata_truncated_from_chars,omitempty"`
	Events           any            `json:"events"`
	EventsCut        *int           `json:"events_truncated_from_chars,omitempty"`
	Links            any            `json:"links"`
	CreatedAt        time.Time      `json:"created_at"`
}

// newSpanRow cuts each of input, output, metadata and events at maxChars
// (0 = complete).
func newSpanRow(sp store.Span, maxChars int) spanRow {
	r := spanRow{TraceID: sp.TraceID, ID: sp.ID, ParentSpanID: sp.ParentSpanID, Kind: sp.Kind, Name: sp.Name,
		StartedAt: sp.StartedAt, EndedAt: sp.EndedAt, Status: sp.Status, StatusMessage: sp.StatusMessage, Model: sp.Model,
		InputTokens: sp.InputTokens, OutputTokens: sp.OutputTokens, CacheReadTokens: sp.CacheReadTokens,
		CacheWriteTokens: sp.CacheWriteTokens, ReasoningTokens: sp.ReasoningTokens, CostUSD: sp.CostUSD,
		PricePattern: sp.PricePattern, PriceFingerprint: sp.PriceFingerprint, CreatedAt: sp.CreatedAt}
	r.Input, r.InputCut = shown(store.Deref(sp.Input), maxChars)
	r.Output, r.OutputCut = shown(store.Deref(sp.Output), maxChars)
	r.Metadata, r.MetadataCut = shown(string(sp.Metadata), maxChars)
	r.Events, r.EventsCut = shown(string(sp.Events), maxChars)
	r.Links, _ = shown(string(sp.Links), 0)
	return r
}

// shown is a stored text as a reader wants it: nil when empty, cut at
// maxChars characters (0 = never) with its full length, else the JSON itself
// when it is a JSON object or array, so a reader does not parse a string twice.
func shown(s string, maxChars int) (any, *int) {
	r := []rune(s)
	switch {
	case s == "":
		return nil, nil
	case maxChars > 0 && len(r) > maxChars:
		n := len(r)
		return string(r[:maxChars]), &n
	case (s[0] == '{' || s[0] == '[') && json.Valid([]byte(s)):
		return json.RawMessage(s), nil
	}
	return s, nil
}

// exportRow is a span line of `spoor export`: the span and its trace's name,
// status, service and session.
type exportRow struct {
	spanRow
	Attributes  any          `json:"attributes"` // every attribute as the sender put it on the span
	Service     *string      `json:"service"`
	SessionID   *string      `json:"session_id"`
	TraceName   string       `json:"trace_name"`
	TraceStatus store.Status `json:"trace_status"`
}

func callTool(ctx context.Context, st store.Store, name string, a toolArgs) (any, error) {
	limit := a.Limit
	if limit <= 0 {
		limit = 20
	}
	switch name {
	case "list_traces":
		return listTraces(ctx, st, a, limit)
	case "get_trace":
		return getTrace(ctx, st, a)
	case "search_spans":
		return searchSpans(ctx, st, a, limit)
	}
	return nil, fmt.Errorf("unknown tool %q", name)
}

func listTraces(ctx context.Context, st store.Store, a toolArgs, limit int) (any, error) {
	q := store.TraceQuery{Service: a.Service, Limit: limit}
	if a.Status != "" {
		s := store.Status(a.Status)
		q.Status = &s
	}
	if a.Since != "" {
		from, err := parseSince(a.Since)
		if err != nil {
			return nil, err
		}
		q.From = &from
	}
	traces, _, err := st.QueryTraces(ctx, q)
	rows := make([]traceRow, len(traces))
	for i, t := range traces {
		rows[i] = newTraceRow(t)
	}
	return map[string]any{"traces": rows}, err
}

// parseSince reads an age (24h, 90m) or a UTC time: RFC 3339 or a date.
func parseSince(s string) (time.Time, error) {
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().UTC().Add(-d), nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("since %q: want an age such as 24h or 90m, an RFC 3339 time or a date such as 2026-10-02", s)
}

func getTrace(ctx context.Context, st store.Store, a toolArgs) (any, error) {
	summary, err := st.GetTraceSummary(ctx, a.TraceID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("no trace with id %q; list_traces returns valid ids", a.TraceID)
	}
	if err != nil {
		return nil, err
	}
	_, spans, err := st.GetTraceByID(ctx, a.TraceID)
	if err != nil {
		return nil, err
	}
	maxChars := 1000
	if a.MaxChars != nil {
		maxChars = *a.MaxChars
	}
	shownSpans := []spanRow{}
	for _, sp := range spans {
		if a.Status == "" || string(sp.Status) == a.Status {
			shownSpans = append(shownSpans, newSpanRow(sp, maxChars))
		}
	}
	result := map[string]any{"trace": newTraceRow(summary), "spans": shownSpans}
	if hidden := len(spans) - len(shownSpans); hidden > 0 {
		result["spans_not_shown"] = fmt.Sprintf("%d spans with another status were left out by the status filter", hidden)
	}
	return result, nil
}

// searchSpans finds the newest traces with a matching span, then their
// matching spans; only the first match of a trace carries a snippet.
func searchSpans(ctx context.Context, st store.Store, a toolArgs, limit int) (any, error) {
	f := store.SpanFilter{Text: a.Q}
	hits := []map[string]any{}
	if !slices.ContainsFunc(store.ParseSearch(a.Q), func(t store.SearchTerm) bool { return !t.Not }) {
		return map[string]any{"spans": hits}, nil
	}
	traces, _, err := st.QueryTraces(ctx, store.TraceQuery{Service: a.Service, Span: f, Limit: limit})
	if err != nil {
		return nil, err
	}
	ids, rank := make([]string, len(traces)), map[string]int{}
	for i, t := range traces {
		ids[i], rank[t.ID] = t.ID, i
	}
	found, err := st.MatchingSpans(ctx, f, ids)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(found, func(i, j int) bool { return rank[found[i].TraceID] < rank[found[j].TraceID] })
	mark := strings.NewReplacer(store.HitStart, "**", store.HitEnd, "**")
	for _, h := range found[:min(len(found), limit)] {
		hit := map[string]any{"trace_id": h.TraceID, "span_id": h.SpanID, "name": h.SpanName, "kind": h.Kind, "status": h.Status}
		if h.Snippet != "" {
			hit["snippet"] = mark.Replace(h.Snippet)
		}
		hits = append(hits, hit)
	}
	return map[string]any{"spans": hits}, nil
}
