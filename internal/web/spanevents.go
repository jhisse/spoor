package web

import (
	"encoding/json"
	"strings"

	"github.com/jhisse/spoor/internal/otlp"
	"github.com/jhisse/spoor/internal/store"
)

// eventView is one span event in the panel. Offset is its distance from
// the span's start, empty when the event carries no usable time.
type eventView struct {
	Name, Offset string
	Attributes   json.RawMessage
	Exception    otlp.Exception
	StackLines   int
}

// spanEventsView is the span panel's error block (status text, exceptions)
// and timeline (every other event, in the order sent), plus the link count.
type spanEventsView struct {
	StatusMessage string
	Exceptions    []eventView
	Events        []eventView
	Links         int
}

func spanEvents(sp *store.Span) (v spanEventsView) {
	v.StatusMessage = store.Deref(sp.StatusMessage)
	var events []store.SpanEvent
	_ = json.Unmarshal(sp.Events, &events) // NULL column: no events
	for _, e := range events {
		ev := eventView{Name: e.Name, Attributes: e.Attributes}
		if d := e.Time.Sub(sp.StartedAt); d >= 0 && !e.Time.After(sp.EndedAt) {
			ev.Offset = "+" + fmtDuration(d)
		}
		var isException bool
		if ev.Exception, isException = otlp.ExceptionOf(e); isException {
			ev.StackLines = strings.Count(strings.TrimSpace(ev.Exception.Stacktrace), "\n") + 1
			v.Exceptions = append(v.Exceptions, ev)
		} else {
			v.Events = append(v.Events, ev)
		}
	}
	var links []json.RawMessage
	_ = json.Unmarshal(sp.Links, &links) // NULL column: no links
	v.Links = len(links)
	return v
}

// errorReason is why a failed span failed, on one line: the status text,
// else the first recorded exception. Empty for a span that did not fail or
// whose sender gave no reason — spoor does not guess one from the output.
func errorReason(sp store.Span) string {
	if sp.Status != store.StatusError {
		return ""
	}
	v := spanEvents(&sp)
	if v.StatusMessage == "" && len(v.Exceptions) > 0 {
		ex := v.Exceptions[0].Exception
		v.StatusMessage = strings.TrimPrefix(ex.Type+": "+ex.Message, ": ")
	}
	return shortText(strings.TrimSuffix(v.StatusMessage, ": "))
}
