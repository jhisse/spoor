package otlp

import "time"

// unixNanoTime converts an OTLP UnixNano timestamp. A value beyond int64
// yields a garbage time, not a crash; plausibility is not validated.
func unixNanoTime(ns uint64) time.Time {
	return time.Unix(0, int64(ns)).UTC() // #nosec G115 -- see doc comment
}
