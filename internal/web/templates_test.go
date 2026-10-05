package web

import (
	"html/template"
	"testing"
	"time"
)

func TestFmtDuration(t *testing.T) {
	cases := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"exact zero", 0, "0 ms"},
		{"sub-millisecond rounds to zero ms, must not read as 0s", 300 * time.Microsecond, "300 µs"},
		{"whole milliseconds", 204 * time.Millisecond, "204 ms"},
		{"just under a second", 999 * time.Millisecond, "999 ms"},
		{"never 1000 ms", 999600 * time.Microsecond, "1.0 s"},
		{"seconds, one decimal", 1202496 * time.Microsecond, "1.2 s"},
		{"multi-second", 2160000 * time.Microsecond, "2.2 s"},
		{"never 60.0 s", 59960 * time.Millisecond, "1 min"},
		{"minutes and seconds", 150990 * time.Millisecond, "2 min 31 s"},
		{"hours drop the seconds", 3723 * time.Second, "1 h 02 min"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fmtDuration(c.d); got != c.want {
				t.Errorf("fmtDuration(%v) = %q, want %q", c.d, got, c.want)
			}
		})
	}
}

func TestRelTime(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		ago  time.Duration
		want string
	}{
		{"just now", 2 * time.Second, "now"},
		{"seconds", 45 * time.Second, "45 s ago"},
		{"minutes", 5 * time.Minute, "5 min ago"},
		{"hours", 3 * time.Hour, "3 h ago"},
		{"days", 2 * 24 * time.Hour, "2 d ago"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := relTime(now.Add(-c.ago)); got != c.want {
				t.Errorf("relTime(now-%v) = %q, want %q", c.ago, got, c.want)
			}
		})
	}
}

// Unknown is not zero: an unreported output is "?", and the exact figures
// stay in the title.
func TestTokenPairAbbreviatesAndKeepsUnknownApart(t *testing.T) {
	zero := int64(0)
	for want, got := range map[string]template.HTML{
		`<span title="1,171,881 in · output not reported by the SDK">1.17M <small>in</small> · ? <small>out</small></span>`: tokenPair(1171881, nil),
		`<span title="1,171,881 in · 0 out">1.17M <small>in</small> · 0 <small>out</small></span>`:                          tokenPair(1171881, &zero),
		`<span title="91,800 in · 950 out">91.8k <small>in</small> · 950 <small>out</small></span>`:                         tokenSum(91800, 950),
		`<span title="63 in · output not reported by the SDK">63 <small>in</small> · ? <small>out</small></span>`:           tokenSum(63, 0),
		"–": tokenSum(0, 0),
	} {
		if string(got) != want {
			t.Errorf("got %s, want %s", got, want)
		}
	}
}

func TestRelTimeBeyondAWeekIsAbsolute(t *testing.T) {
	old := time.Date(2020, 1, 15, 9, 30, 0, 0, time.UTC)
	if got, want := relTime(old), "2020-01-15 09:30"; got != want {
		t.Errorf("relTime(old) = %q, want %q", got, want)
	}
}

// A real cost too small for four decimals must not read as free.
func TestUSD4(t *testing.T) {
	for v, want := range map[float64]string{0: "$0.0000", 0.00002: "<$0.0001", 0.00005: "$0.0001", 0.5633: "$0.5633"} {
		if got := usd4(v); got != want {
			t.Errorf("usd4(%v) = %q, want %q", v, got, want)
		}
	}
}
