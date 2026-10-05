package web

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"strings"

	"github.com/jhisse/spoor/internal/store"
)

type sessionPageData struct {
	shell
	Service   string // the list's ?service= filter
	Sessions  []sessionRow
	MaxTokens int64 // the page's largest session, the token bars' common scale
	Legend    template.HTML
	NextURL   string
	Replay    *sessionReplay
}

type sessionRow struct {
	store.SessionSummary
	Mix     TokenMix
	Subject string // the first prompt of the first turn, cut short; "" when no turn recorded one
}

// subjects reads what each trace is about with pick, from its first model
// call's messages or, for a trace whose calls carry no body, from the
// prompt on its agent span (where a coding agent records what the user
// typed). Keyed by trace id.
func (h *Handlers) subjects(ctx context.Context, firstTraceIDs []string, pick func(*string) string) (map[string]string, error) {
	out := map[string]string{}
	q := store.SpanQuery{TraceIDs: firstTraceIDs, First: true, Bodies: true}
	for _, kind := range []store.SpanKind{store.SpanKindGeneration, store.SpanKindAgent} {
		q.Kind = kind
		spans, err := h.Store.QuerySpans(ctx, q)
		if err != nil {
			return nil, err
		}
		for _, sp := range spans {
			if out[sp.TraceID] == "" {
				out[sp.TraceID] = pick(sp.Input)
			}
		}
	}
	return out, nil
}

// subjectText is the first user message with content, what the person
// asked; else previewText's choice.
func subjectText(raw *string) string {
	for _, m := range parseMessages(raw) {
		if m.Role == "user" && m.Content != "" {
			return shortText(m.Content)
		}
	}
	return previewText(raw)
}

// askedFull is the last user message with content, what a trace answered,
// in one line; else lastText's choice.
func askedFull(raw *string) string {
	msgs := parseMessages(raw)
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" && msgs[i].Content != "" {
			return oneLine(msgs[i].Content)
		}
	}
	return lastText(raw)
}

// askedText is askedFull cut short, for a row.
func askedText(raw *string) string { return shortText(askedFull(raw)) }

// Wall is first start to last end, the session's wall clock.
func (s sessionRow) Wall() string { return fmtCoarse(s.EndedAt.Sub(s.StartedAt)) }

type sessionReplay struct {
	Summary    store.SessionSummary
	Turns      []sessionTurn
	Work, Wall string // sum of trace durations vs. first start to last end
	MaxTokens  int64
	StripScale string        // what column height means; "" for a one-turn session, which has no strip
	Strip      template.HTML // the turn strip, one column per turn
	Legend     template.HTML
	Chart      *contextChart // nil below two generations
}

// Sessions renders GET /sessions: the conversation sessions, optionally
// narrowed by ?service=, or one of them with ?session=. A session id stays
// in the query string because OTLP permits arbitrary string values that are
// not reliable as one URL path segment.
func (h *Handlers) Sessions(w http.ResponseWriter, r *http.Request) {
	data := sessionPageData{Service: r.URL.Query().Get("service")}
	var err error
	if id := r.URL.Query().Get("session"); id == "" {
		err = h.sessionList(r, &data)
	} else {
		err = h.sessionReplay(r.Context(), id, &data)
	}
	if errors.Is(err, store.ErrNotFound) {
		h.NotFound(w, r)
		return
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	h.renderPage(w, r, sessionsTmpl, data.Service, &data)
}

func (h *Handlers) sessionList(r *http.Request, data *sessionPageData) error {
	ctx := r.Context()
	sessions, next, err := h.Store.QuerySessions(ctx, data.Service, parseListParams(r.URL.Query()).Cursor, defaultLimit)
	if err != nil {
		return err
	}
	var total TokenMix
	firsts := make([]string, len(sessions))
	for i, ss := range sessions {
		firsts[i] = ss.FirstTraceID
		mix := summaryMix(ss.TotalInputTokens, ss.TotalOutputTokens, ss.TotalCacheReadTokens, ss.TotalCacheWriteTokens)
		data.Sessions = append(data.Sessions, sessionRow{SessionSummary: ss, Mix: mix})
		data.MaxTokens = max(data.MaxTokens, mix.Total())
		total = total.Add(mix)
	}
	subjects, err := h.subjects(ctx, firsts, subjectText)
	if err != nil {
		return err
	}
	for i := range data.Sessions {
		data.Sessions[i].Subject = subjects[data.Sessions[i].FirstTraceID]
	}
	data.Legend = legend(total, false)
	data.NextURL = buildNextURL(r.URL, next)
	return nil
}

func (h *Handlers) sessionReplay(ctx context.Context, sessionID string, data *sessionPageData) error {
	summary, traces, err := h.Store.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	// The chart needs every model call but no body; the previews need a
	// body but only each turn's first call. A coding agent's calls each
	// repeat the whole conversation, so all bodies would be the session
	// many times over.
	inSession := store.SpanQuery{SessionID: sessionID, Kind: store.SpanKindGeneration}
	gens, err := h.Store.QuerySpans(ctx, inSession)
	if err != nil {
		return err
	}
	inSession.Bodies, inSession.First = true, true
	firsts, err := h.Store.QuerySpans(ctx, inSession)
	if err != nil {
		return err
	}
	prices, err := h.Store.ListModelPrices(ctx)
	if err != nil {
		return err
	}
	data.Replay = buildReplay(summary, traces)
	data.Replay.Chart = buildSessionChart(traces, gens, prices)
	turns := map[string]*sessionTurn{}
	for i := range data.Replay.Turns {
		turns[traces[i].ID] = &data.Replay.Turns[i]
	}
	// A turn previews its first generation span: the newest prompt
	// message going in, the last completion coming out.
	for _, first := range firsts {
		if turn := turns[first.TraceID]; turn != nil {
			turn.Input, turn.Output = previewText(first.Input), previewText(first.Output)
		}
	}
	return h.turnPrompts(ctx, inSession, turns)
}

func previewText(raw *string) string { return shortText(lastText(raw)) }

// lastText is the content of the last message that has one, in one line; a
// prompt that is not a list of messages is the text itself.
func lastText(raw *string) string {
	if raw == nil {
		return ""
	}
	msgs := parseMessages(raw)
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Content != "" {
			return oneLine(msgs[i].Content)
		}
	}
	if msgs != nil {
		return ""
	}
	return oneLine(*raw)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func shortText(value string) string { return clipHead([]rune(oneLine(value)), 180) }
