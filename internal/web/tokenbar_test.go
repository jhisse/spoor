package web

import (
	"strings"
	"testing"

	"github.com/jhisse/spoor/internal/store"
)

func TestMixOfSplitsCacheOutOfInput(t *testing.T) {
	in, out, read, write := int64(88752), int64(40), int64(87486), int64(1264)
	m := store.Span{InputTokens: &in, OutputTokens: &out, CacheReadTokens: &read, CacheWriteTokens: &write}.Mix()
	if m.Fresh != 2 || m.CacheRead != read || m.CacheWrite != write || m.Output != out {
		t.Errorf("mix = %+v, want fresh 2 (input minus both cache buckets)", m)
	}
	if m.Total() != in+out {
		t.Errorf("Total = %d, want %d", m.Total(), in+out)
	}
}

func TestMixOfFloorsFreshWhenBucketsAreDisjoint(t *testing.T) {
	in, read := int64(10), int64(500)
	if m := (store.Span{InputTokens: &in, CacheReadTokens: &read}).Mix(); m.Fresh != 0 {
		t.Errorf("Fresh = %d, want 0", m.Fresh)
	}
}

func TestTokenBar(t *testing.T) {
	if got := tokenBar(TokenMix{}, 0, 100, 8); got != "" {
		t.Errorf("empty mix rendered %q, want nothing", got)
	}
	got := string(tokenBar(TokenMix{CacheRead: 50, Output: 50}, 200, 100, 8))
	if strings.Count(got, "<rect") != 2 {
		t.Errorf("want one rect per non-zero bucket, got: %s", got)
	}
	// 50 of a 200 scale on 100px is 25px: 24 drawn and a 1px gap before the next.
	if !strings.Contains(got, `width="24.00"`) || !strings.Contains(got, `<rect x="25.00"`) {
		t.Errorf("want a 24px segment and the next one starting at 25, got: %s", got)
	}
	if !strings.Contains(got, `fill="url(#hatch)"`) || !strings.Contains(got, `class="tk-out"`) || strings.Contains(got, "style=") {
		t.Errorf("cache read is the hatch, output a class, nothing inline: %s", got)
	}
}

// The context delta prints a negative difference: the sign is not a digit.
func TestCommas(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 1171881: "1,171,881", -123: "-123", -1234: "-1,234", -123456: "-123,456"} {
		if got := commas(n); got != want {
			t.Errorf("commas(%d) = %q, want %q", n, got, want)
		}
	}
}
