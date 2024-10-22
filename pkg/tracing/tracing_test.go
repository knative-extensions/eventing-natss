package tracing

import (
	"context"
	"testing"

	"github.com/cloudevents/sdk-go/v2/binding"
	"go.opencensus.io/trace"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

const data = `{"specversion":"1.0","type":"type","source":"source","id":"1234-1234-1234","data":{"firstName":"John"}}`
const traceId = "8abe1a4854a9864ffa63046ef07b5dbe"
const tp = "00-" + traceId + "-8829876d85d5a76d-01"
const ts = "rojo=00f067aa0ba902b7"

func TestConvertEventToHttpHeader(t *testing.T) {
	event := cloudevents.NewEvent()
	event.SetExtension(traceParentHeader, tp)
	event.SetExtension(traceStateHeader, ts)

	headers := ConvertEventToHttpHeader(&event)
	if headers.Get(traceParentHeader) != tp {
		t.Fatalf("%s header mismatch", traceParentHeader)
	}
	if headers.Get(traceStateHeader) != ts {
		t.Fatalf("%s header mismatch", traceStateHeader)
	}
}

func TestConvertEventToHttpHeaderEmptyEvent(t *testing.T) {
	event := cloudevents.NewEvent()
	headers := ConvertEventToHttpHeader(&event)
	if headers.Get(traceParentHeader) != "" {
		t.Fatalf("%s header must be empty", traceParentHeader)
	}
	if headers.Get(traceStateHeader) != "" {
		t.Fatalf("%s header must be empty", traceStateHeader)
	}
}

func TestConvertNatsMsgToEventIsNotNullableIfNil(t *testing.T) {
	message := ConvertNatsMsgToEvent(zap.NewNop(), nil)
	if message == nil {
		t.Fatalf("Message must be non-nil")
	}
}

func TestConvertNatsMsgToEventIsNotNullableEmptyData(t *testing.T) {
	msg := nats.NewMsg("subject")
	msg.Data = []byte("{}")
	message := ConvertNatsMsgToEvent(zap.NewNop(), msg)
	if message == nil {
		t.Fatalf("Message must be non-nil")
	}
}

func TestConvertNatsMsgToEventIsNotNullableData(t *testing.T) {
	msg := nats.Msg{}
	msg.Data = []byte(data)
	message := ConvertNatsMsgToEvent(zap.NewNop(), &msg)
	if message == nil {
		t.Fatalf("Message must be non-nil")
	}
}

func TestStartTraceFromMessage(t *testing.T) {
	msg := cloudevents.NewEvent()
	msg.SetExtension(traceParentHeader, tp)
	msg.SetExtension(traceStateHeader, ts)
	ctx, span := StartTraceFromMessage(zap.NewNop(), context.Background(), &msg, "span-name")
	tc := trace.FromContext(ctx)
	if traceId != tc.SpanContext().TraceID.String() {
		t.Fatalf("TraceId is incorrect, expected: %v, actual: %v", traceId, tc.SpanContext().TraceID)
	}
	if span == nil {
		t.Fatalf("Span must be non-nil")
	}
}

func TestStartTraceFromMessageIsNil(t *testing.T) {
	ctx, span := StartTraceFromMessage(zap.NewNop(), context.Background(), nil, "span-name")
	tc := trace.FromContext(ctx)
	if traceId == tc.SpanContext().TraceID.String() {
		t.Fatalf("TraceId must be new")
	}
	if span == nil {
		t.Fatalf("Span must be non-nil")
	}
}

func TestStartTraceFromMessageTraceParentIsNil(t *testing.T) {
	msg := cloudevents.NewEvent()
	ctx, span := StartTraceFromMessage(zap.NewNop(), context.Background(), &msg, "span-name")
	tc := trace.FromContext(ctx)
	if traceId == tc.SpanContext().TraceID.String() {
		t.Fatalf("TraceId must be new")
	}
	if span == nil {
		t.Fatalf("Span must be non-nil")
	}
}

func TestStartTraceFromMessageTraceStateIsNil(t *testing.T) {
	msg := cloudevents.NewEvent()
	msg.SetExtension(traceParentHeader, tp)
	ctx, span := StartTraceFromMessage(zap.NewNop(), context.Background(), &msg, "span-name")
	tc := trace.FromContext(ctx)
	if traceId != tc.SpanContext().TraceID.String() {
		t.Fatalf("TraceId is incorrect, expected: %v, actual: %v", traceId, tc.SpanContext().TraceID)
	}
	if span == nil {
		t.Fatalf("Span must be non-nil")
	}
}

func TestSerializeTraceTransformers(t *testing.T) {
	msg := cloudevents.NewEvent()
	msg.SetExtension(traceParentHeader, tp)
	msg.SetExtension(traceStateHeader, ts)
	sc, _ := format.SpanContextFromHeaders(tp, ts)
	transformers := SerializeTraceTransformers(sc)
	message := binding.ToMessage(&msg)
	event, _ := binding.ToEvent(context.Background(), message, transformers...)
	if tp != event.Extensions()[traceParentHeader] {
		t.Fatalf("Traceparent is incorrect, expected: %v, actual: %v", tp, event.Extensions()[traceParentHeader])
	}
	if ts != event.Extensions()[traceStateHeader] {
		t.Fatalf("Tracestate is incorrect, expected: %v, actual: %v", tp, event.Extensions()[traceStateHeader])
	}
}
