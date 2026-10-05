package otlp

import (
	"encoding/base64"
	"math"
	"strconv"

	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
)

// stringAttrs flattens OTLP attributes into a string map: every value this
// layer reads is a string, or a number/bool that round-trips through
// Format/Parse.
func stringAttrs(attrs []*commonv1.KeyValue) map[string]string {
	out := make(map[string]string, len(attrs))
	for _, kv := range attrs {
		if s, ok := anyValueToString(kv.Value); ok {
			out[kv.Key] = s
		}
	}
	return out
}

// jsonAttrs keeps every OTLP attribute in a JSON-safe shape, so metadata
// does not lose the arrays, nested lists and binary values stringAttrs drops.
func jsonAttrs(attrs []*commonv1.KeyValue) map[string]any {
	out := make(map[string]any, len(attrs))
	for _, kv := range attrs {
		if v, ok := anyValueToJSON(kv.Value); ok {
			out[kv.Key] = v
		}
	}
	return out
}

func anyValueToJSON(v *commonv1.AnyValue) (any, bool) {
	if v == nil {
		return nil, false
	}
	switch x := v.Value.(type) {
	case *commonv1.AnyValue_StringValue:
		return x.StringValue, true
	case *commonv1.AnyValue_IntValue:
		return x.IntValue, true
	case *commonv1.AnyValue_DoubleValue:
		if math.IsNaN(x.DoubleValue) || math.IsInf(x.DoubleValue, 0) {
			// JSON has no such number, and one of them would fail the whole object.
			return strconv.FormatFloat(x.DoubleValue, 'g', -1, 64), true
		}
		return x.DoubleValue, true
	case *commonv1.AnyValue_BoolValue:
		return x.BoolValue, true
	case *commonv1.AnyValue_BytesValue:
		return base64.StdEncoding.EncodeToString(x.BytesValue), true
	case *commonv1.AnyValue_ArrayValue:
		out := make([]any, 0, len(x.ArrayValue.Values))
		for _, item := range x.ArrayValue.Values {
			if converted, ok := anyValueToJSON(item); ok {
				out = append(out, converted)
			}
		}
		return out, true
	case *commonv1.AnyValue_KvlistValue:
		out := make(map[string]any, len(x.KvlistValue.Values))
		for _, item := range x.KvlistValue.Values {
			if converted, ok := anyValueToJSON(item.Value); ok {
				out[item.Key] = converted
			}
		}
		return out, true
	default:
		return nil, false
	}
}

func anyValueToString(v *commonv1.AnyValue) (string, bool) {
	if v == nil {
		return "", false
	}
	switch x := v.Value.(type) {
	case *commonv1.AnyValue_StringValue:
		return x.StringValue, true
	case *commonv1.AnyValue_IntValue:
		return strconv.FormatInt(x.IntValue, 10), true
	case *commonv1.AnyValue_DoubleValue:
		return strconv.FormatFloat(x.DoubleValue, 'f', -1, 64), true
	case *commonv1.AnyValue_BoolValue:
		return strconv.FormatBool(x.BoolValue), true
	default:
		return "", false
	}
}

// firstPresent returns the first non-empty value among keys; callers pass
// the most authoritative key first.
func firstPresent(attrs map[string]string, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := attrs[k]; ok && v != "" {
			return v, true
		}
	}
	return "", false
}

func firstInt64(attrs map[string]string, keys ...string) (int64, bool) {
	if v, ok := firstPresent(attrs, keys...); ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

func firstFloat64(attrs map[string]string, keys ...string) (float64, bool) {
	if v, ok := firstPresent(attrs, keys...); ok {
		// NaN and Inf parse, and are no amount: a cost of +Inf is not a cost.
		if f, err := strconv.ParseFloat(v, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f, true
		}
	}
	return 0, false
}
