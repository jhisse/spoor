package otlp

import (
	"encoding/hex"

	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
)

func stringAttr(key, value string) *commonv1.KeyValue {
	return &commonv1.KeyValue{
		Key:   key,
		Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: value}},
	}
}

func spoorCostAttr(cost float64) *commonv1.KeyValue {
	return &commonv1.KeyValue{
		Key:   reservedCostAttr,
		Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_DoubleValue{DoubleValue: cost}},
	}
}

func newTestSpan(traceID, spanID, parentSpanID string, startedAt, endedAt uint64, statusCode tracev1.Status_StatusCode, attrs ...*commonv1.KeyValue) *tracev1.Span {
	s := &tracev1.Span{
		TraceId:           mustHexDecode(traceID),
		SpanId:            mustHexDecode(spanID),
		Name:              "test-span",
		StartTimeUnixNano: startedAt,
		EndTimeUnixNano:   endedAt,
		Status:            &tracev1.Status{Code: statusCode},
		Attributes:        attrs,
	}
	if parentSpanID != "" {
		s.ParentSpanId = mustHexDecode(parentSpanID)
	}
	return s
}

func mustHexDecode(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}
