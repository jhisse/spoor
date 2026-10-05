package main

import (
	"context"
	"flag"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"time"

	"github.com/jhisse/spoor/internal/config"
	"github.com/jhisse/spoor/internal/otlp"
	"github.com/jhisse/spoor/internal/store"
	"github.com/jhisse/spoor/internal/store/sqlite"
)

func runReprice(args []string) error {
	fs := flag.NewFlagSet("reprice", flag.ContinueOnError)
	var q store.SpanQuery
	fs.StringVar(&q.Service, "service", "", "only this service's traces")
	bound := func(dst **time.Time) func(string) error {
		return func(raw string) error {
			at, err := time.Parse(time.RFC3339, raw)
			*dst = &at
			return err
		}
	}
	fs.Func("from", "only spans started at or after this RFC 3339 time", bound(&q.From))
	fs.Func("to", "only spans started at or before this RFC 3339 time", bound(&q.To))
	dryRun := fs.Bool("dry-run", false, "report what would change, write nothing")
	fs.Usage = func() {
		_, _ = fmt.Fprint(fs.Output(), `usage: spoor reprice [--service=<name>] [--from=<time>] [--to=<time>] [--dry-run]

Recomputes spans.cost_usd from the current model_prices: what to run after
the price table changes. A cost reported by the sender (spoor.cost_usd,
llm.cost.total) is left alone, and a known cost never becomes unknown.
`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := config.SQLitePath()
	if err != nil {
		return err
	}
	st, err := sqlite.Open(path)
	if err != nil {
		return fmt.Errorf("reprice: %w", err)
	}
	defer func() { _ = st.Close() }()
	if err := reprice(context.Background(), st, q, *dryRun); err != nil {
		return fmt.Errorf("reprice: %w", err)
	}
	return nil
}

func reprice(ctx context.Context, st store.Store, q store.SpanQuery, dryRun bool) error {
	prices, err := st.ListModelPrices(ctx)
	if err != nil {
		return err
	}
	// Every matching span (without bodies) is held in memory and
	// written in one transaction; page by started_at if a store outgrows that.
	spans, err := st.QuerySpans(ctx, q)
	if err != nil {
		return err
	}
	var changed []store.Span
	var before, after float64
	byModel := map[string]*modelChange{} // the changed spans, by model
	for _, sp := range spans {
		repriced := otlp.Reprice(prices, sp)
		before, after = before+store.Deref(sp.CostUSD), after+store.Deref(repriced.CostUSD)
		if reflect.DeepEqual(sp, repriced) {
			continue
		}
		changed = append(changed, repriced)
		if repriced.Model == nil {
			continue
		}
		c := byModel[*repriced.Model]
		if c == nil {
			c = &modelChange{from: "no recorded row", to: "no recorded row"}
			byModel[*repriced.Model] = c
		}
		c.spans, c.before, c.after = c.spans+1, c.before+store.Deref(sp.CostUSD), c.after+store.Deref(repriced.CostUSD)
		if sp.PricePattern != nil {
			c.from = *sp.PricePattern
		}
		if repriced.PricePattern != nil {
			c.to = *repriced.PricePattern
		}
	}
	verb := "changed"
	if dryRun {
		verb = "would change"
	} else if err := st.UpdateSpanUsage(ctx, changed); err != nil {
		return err
	}
	fmt.Printf("reprice: %d of %d spans %s; total cost $%.6f -> $%.6f\n", len(changed), len(spans), verb, before, after)
	for _, model := range slices.Sorted(maps.Keys(byModel)) {
		c := byModel[model]
		fmt.Printf("  %s: %d spans, $%.6f -> $%.6f; price row: %s -> %s\n", model, c.spans, c.before, c.after, c.from, c.to)
	}
	return nil
}

type modelChange struct {
	spans         int
	before, after float64
	from, to      string // price rows: the one recorded on the spans, the one that prices them now
}
