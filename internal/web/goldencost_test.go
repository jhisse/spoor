package web

import (
	"html/template"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/jhisse/spoor/internal/otlp"
	"github.com/jhisse/spoor/internal/store"
)

// Golden totals: every capture, from the OTLP bytes through the ingestion
// handler and the price catalog to the stored cost_usd and to the
// number in the HTML. Each expected cost is written out by hand (tokens per
// bucket, summed over the capture's calls of one model, times the published
// price per million), never computed by the code under test.
//
// Buckets, in order: fresh input, cache read, 5-minute cache write, output.
const (
	haiku45   = "haiku 4.5: $1, $0.10, $1.25, $5"
	sonnet5   = "sonnet 5: $2, $0.20, $2.50, $10"
	opus55    = "opus 5.5: $4, $0.20, $5, $20"
	gpt4oMini = "gpt-4o-mini: $0.15, –, –, $0.60"
	gemini36  = "gemini 3.6 flash: $0.75, –, –, $3.75"

	gpt4oMiniCached = "gpt-4o-mini: $0.15, $0.075, –, $0.60"
	gpt5Mini        = "gpt-5-mini: $0.25, $0.025, $0.25, $2"
)

var goldenCaptures = []struct {
	file  string
	why   string
	total float64 // USD; 0 = no priced model, cost unknown
	// html are exact strings the pages must show (list at 4 decimals, the
	// span's arithmetic at 8).
	html []string
}{
	{"claude-code-simple.pb", haiku45,
		(60*1 + 26894*0.10 + 13348*1.25 + 120*5) / 1e6, // = 0.0200344
		// The first call wrote 13,348 cache tokens: at the 1-hour rate ($2/M, not
		// $1.25/M) it costs 0.0193633 + 13348*0.75/1e6 = 0.0293743. The trace
		// does not say which, so the page shows the range.
		[]string{"$0.0200", "$0.01936330", "$0.00067110", "cache write, 5-minute rate",
			"<b>$0.01936330–$0.02937430</b>: the trace does not say whether cache writes were 5-minute or 1-hour", "at the 1-hour rate ($2/M) this call costs $0.02937430"}},
	{"claude-code-tool-read.pb", haiku45,
		(81*1 + 66756*0.10 + 9460*1.25 + 224*5) / 1e6, // = 0.0197016
		[]string{"$0.0197", "$0.01496625"}},
	// Opus 5.5 sent as "claude-opus-5-5[1m]": priced by its own row.
	{"claude-code-interactive-parallel.pb", opus55 + "; " + sonnet5,
		(8*4+265632*0.20+21266*5+2810*20)/1e6 + // opus 5.5 = 0.2156884
			(90*2+69334*0.20+377*2.50+9*10)/1e6, // sonnet 5 = 0.0150793
		nil},
	{"claude-code-interactive-tools.pb", haiku45 + "; " + opus55 + "; " + sonnet5,
		(895*1+13*5)/1e6 + // haiku = 0.00096
			(6*4+180079*0.20+56824*5+1984*20)/1e6 + // opus 5.5 = 0.3598398
			(270*2+138415*0.20+69587*2.50+27*10)/1e6, // sonnet 5 = 0.2024605
		nil},
	{"openllmetry-anthropic-simple.pb", haiku45, (19*1 + 10*5) / 1e6, []string{"$0.00006900"}},
	{"openllmetry-anthropic-tool-use.pb", haiku45, (583*1 + 56*5) / 1e6, []string{"$0.0009", "$0.00086300"}},
	{"openllmetry-anthropic-tool-use-2.pb", haiku45, (623*1 + 92*5) / 1e6, []string{"$0.0011"}},
	{"openllmetry-anthropic-multiturn-1.pb", haiku45, (12*1 + 5*5) / 1e6, nil},
	{"openllmetry-anthropic-multiturn-2.pb", haiku45, (31*1 + 5*5) / 1e6, nil},
	{"openllmetry-anthropic-sonnet-simple.pb", sonnet5, (20*2 + 100*10) / 1e6, []string{"$0.0010", "$0.00104000"}},
	{"openllmetry-langgraph-tool-loop.pb", haiku45, (1224*1 + 83*5) / 1e6, []string{"$0.0016"}},
	{"openllmetry-llamaindex-rag-query.pb", gpt4oMini, (188*0.15 + 28*0.60) / 1e6, []string{"$0.00004500"}},
	{"openllmetry-openai-openrouter-simple.pb", gpt4oMini, (19*0.15 + 8*0.60) / 1e6, []string{"$0.00000765"}},
	{"openllmetry-openai-openrouter-tool-use.pb", gpt4oMini, (63*0.15 + 14*0.60) / 1e6, nil},
	{"openllmetry-openai-openrouter-tool-search.pb", gpt4oMini, (58*0.15 + 19*0.60) / 1e6, nil},
	{"openllmetry-openai-openrouter-multiturn-1.pb", gpt4oMini, (13*0.15 + 8*0.60) / 1e6, nil},
	{"openllmetry-openai-openrouter-multiturn-2.pb", gpt4oMini, (35*0.15 + 11*0.60) / 1e6, nil},
	{"openllmetry-openrouter-gemini-simple.pb", gemini36, (13*0.75 + 196*3.75) / 1e6, []string{"$0.0007", "$0.00074475"}},

	// From testdata/capture: its fake provider answers every OpenAI call with
	// 1200 prompt tokens (1024 of them cached) and 40 output, every Anthropic
	// call with 20 fresh, 1000 cache read, 200 cache write and 5 output. The
	// tool captures make two calls. litellm-otel-tool is not here: it reports
	// its own cost (TestCostBreakdownClosesOnEveryRealCapture).
	{"openinference-openai-plain.pb", gpt4oMiniCached, (176*0.15 + 1024*0.075 + 40*0.60) / 1e6, []string{"$0.0001", "$0.00012720"}},
	{"openinference-openai-tool.pb", gpt4oMiniCached, 2 * (176*0.15 + 1024*0.075 + 40*0.60) / 1e6, []string{"$0.0003"}},
	{"openinference-langchain-tool.pb", gpt4oMiniCached, 2 * (176*0.15 + 1024*0.075 + 40*0.60) / 1e6, nil},
	{"openinference-crewai-workflow.pb", gpt4oMiniCached, 2 * (176*0.15 + 1024*0.075 + 40*0.60) / 1e6, nil},
	{"openinference-llamaindex-tool.pb", gpt4oMiniCached, 2 * (176*0.15 + 1024*0.075 + 40*0.60) / 1e6, nil},
	{"traceloop-openai-plain.pb", gpt4oMiniCached, (176*0.15 + 1024*0.075 + 40*0.60) / 1e6, nil},
	{"traceloop-openai-tool.pb", gpt4oMiniCached, 2 * (176*0.15 + 1024*0.075 + 40*0.60) / 1e6, nil},
	{"pydantic-ai-tool.pb", gpt4oMiniCached, 2 * (176*0.15 + 1024*0.075 + 40*0.60) / 1e6, nil},
	// The root invoke_agent span repeats its two calls' usage; it is not counted.
	{"vercel-ai-tool.pb", gpt4oMiniCached, 2 * (176*0.15 + 1024*0.075 + 40*0.60) / 1e6, nil},
	// These two senders put no cached-token count on the span: all 1200 are priced as fresh.
	{"openinference-openai-agents-tool.pb", gpt4oMiniCached, 2 * (1200*0.15 + 40*0.60) / 1e6, []string{"$0.00020400"}},
	{"otel-openai-v2-tool.pb", gpt4oMiniCached, 2 * (1200*0.15 + 40*0.60) / 1e6, nil},
	{"otel-openai-v2-events.pb", gpt4oMiniCached, (1200*0.15 + 40*0.60) / 1e6, nil},
	{"openinference-anthropic-plain.pb", haiku45, (20*1 + 1000*0.10 + 200*1.25 + 5*5) / 1e6, []string{"$0.00039500"}},
	{"openinference-anthropic-tool.pb", haiku45, 2 * (20*1 + 1000*0.10 + 200*1.25 + 5*5) / 1e6, nil},
	{"traceloop-anthropic-plain.pb", haiku45, (20*1 + 1000*0.10 + 200*1.25 + 5*5) / 1e6, nil},
	{"traceloop-anthropic-tool.pb", haiku45, 2 * (20*1 + 1000*0.10 + 200*1.25 + 5*5) / 1e6, nil},
	// Gemini CLI: the fake's 1024 cached and 32 thinking tokens are not on the span.
	{"gemini-cli-plain.pb", gemini36, (1200*0.75 + 8*3.75) / 1e6, []string{"$0.0009", "$0.00093000"}},
	// Real models through OpenRouter (testdata/chatbots): two or three calls each, summed by hand.
	{"chatbot-plain-openai.pb", gpt4oMini, (422*0.15 + 75*0.60) / 1e6, nil},
	{"chatbot-openai-agents.pb", gpt4oMini, (608*0.15 + 91*0.60) / 1e6, nil},
	{"chatbot-llamaindex.pb", gpt4oMini, (875*0.15 + 100*0.60) / 1e6, nil},
	{"chatbot-pydanticai.pb", gpt4oMini, (474*0.15 + 72*0.60) / 1e6, nil},
	// The agent span repeats its two calls' usage: it is not counted.
	{"chatbot-smolagents.pb", gpt4oMini, (3049*0.15 + 111*0.60) / 1e6, nil},
	// Reasoning tokens are part of the output tokens.
	{"chatbot-plain-openai-reasoning.pb", gpt5Mini, (531*0.25 + 368*2) / 1e6, nil},
	{"chatbot-llamaindex-reasoning.pb", gpt5Mini, (621*0.25 + 335*2) / 1e6, nil},
	{"chatbot-pydanticai-reasoning.pb", gpt5Mini, (473*0.25 + 255*2) / 1e6, nil},
	{"chatbot-plain-anthropic.pb", haiku45, (1748*1 + 134*5) / 1e6, nil},
	{"chatbot-plain-anthropic-thinking.pb", haiku45, (1886*1 + 213*5) / 1e6, nil},
	// A real prompt cache: the first call wrote 4,796 tokens, the second read them.
	{"chatbot-langchain.pb", haiku45, ((5135-4796)*1 + 4796*1.25 + 94*5 + (5307-4796)*1 + 4796*0.10 + 36*5) / 1e6, nil},
	// A failed call has no usage: no cost.
	{"openinference-openai-error.pb", "no usage", 0, nil},
	{"openinference-anthropic-error.pb", "no usage", 0, nil},
	{"traceloop-openai-error.pb", "no usage", 0, nil},
	{"otel-openai-v2-error.pb", "no usage", 0, nil},
}

func TestGoldenCostOfEveryCapture(t *testing.T) {
	for _, c := range goldenCaptures {
		t.Run(c.file, func(t *testing.T) {
			s := newTestStore(t)
			h := newTestHandlers(t, s)
			ingestCapture(t, s, c.file)
			prices, err := s.ListModelPrices(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			traces, _, err := s.QueryTraces(t.Context(), store.TraceQuery{Limit: 50})
			if err != nil || len(traces) == 0 {
				t.Fatalf("QueryTraces: %v, %d traces", err, len(traces))
			}
			list := doList(t, h, "").Body.String()
			var stored float64
			var pages strings.Builder
			pages.WriteString(list)
			for _, tr := range traces {
				_, spans, err := s.GetTraceByID(t.Context(), tr.ID)
				if err != nil {
					t.Fatal(err)
				}
				var traceCost float64
				for _, sp := range spans {
					if sp.CostUSD == nil {
						continue
					}
					traceCost += *sp.CostUSD
					page := doDetailQuery(t, h, tr.ID, "span="+sp.ID).Body.String()
					pages.WriteString(page)
					checkSpanInvariants(t, sp, prices, page)
				}
				stored += traceCost
				if want := template.HTMLEscapeString(usd4(traceCost)); traceCost != 0 && !strings.Contains(list, want) {
					t.Errorf("trace list does not show %s for trace %s", want, tr.Name)
				}
			}
			// Exact for the unpriced capture: no span may carry any cost.
			if math.Abs(stored-c.total) > 1e-12 || (c.total == 0) != (stored == 0) {
				t.Errorf("stored cost = $%.10f, by hand $%.10f (%s)", stored, c.total, c.why)
			}
			for _, want := range c.html {
				if !strings.Contains(pages.String(), want) {
					t.Errorf("no page shows %q", want)
				}
			}
		})
	}
}

// The invariants of one priced span: the four buckets are the whole usage;
// the arithmetic on screen sums to the stored cost and shows it; repricing
// with the same table changes nothing.
func checkSpanInvariants(t *testing.T, sp store.Span, prices []store.ModelPrice, page string) {
	t.Helper()
	m := sp.Mix()
	if got, want := m.Fresh+m.CacheRead+m.CacheWrite, store.Deref(sp.InputTokens); got != want {
		t.Errorf("%s: fresh+cache read+cache write = %d, input tokens = %d", sp.Name, got, want)
	}
	if got, want := m.Total(), store.Deref(sp.InputTokens)+store.Deref(sp.OutputTokens); got != want {
		t.Errorf("%s: buckets sum to %d, input+output = %d", sp.Name, got, want)
	}
	b := buildCostBreakdown(sp, prices)
	var lines float64
	for _, l := range b.Lines {
		lines += l.USD
	}
	if math.Abs(lines-*sp.CostUSD) > 1e-12 || math.Abs(b.Computed-*sp.CostUSD) > 1e-12 || b.Differs() {
		t.Errorf("%s: breakdown lines sum to %v (computed %v), stored cost %v", sp.Name, lines, b.Computed, *sp.CostUSD)
	}
	if store.Deref(sp.PricePattern) != b.Pattern || b.Stale || sp.PriceFingerprint == nil {
		t.Errorf("%s: recorded price row %q, breakdown uses %q (stale %v)", sp.Name, store.Deref(sp.PricePattern), b.Pattern, b.Stale)
	}
	// A range exactly when an Anthropic model wrote cache: nobody else has two write rates.
	ranged := m.CacheWrite > 0 && strings.Contains(*sp.Model, "claude")
	if (b.Upper > *sp.CostUSD) != ranged || strings.Contains(page, "5-minute or 1-hour") != ranged {
		t.Errorf("%s: upper bound %v for a stored cost of %v with %d cache write tokens", sp.Name, b.Upper, *sp.CostUSD, m.CacheWrite)
	}
	if want := "= " + usd(*sp.CostUSD); !strings.Contains(page, want) {
		t.Errorf("%s: span page does not show the sum %q", sp.Name, want)
	}
	if again := otlp.Reprice(prices, sp); !reflect.DeepEqual(again, sp) {
		t.Errorf("%s: repricing a freshly ingested span changed it: %+v -> %+v", sp.Name, sp.CostUSD, again.CostUSD)
	}
}
