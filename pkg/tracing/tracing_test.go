package tracing

import (
	"context"
	"testing"

	"github.com/cloudevents/sdk-go/v2/binding"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

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
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	ctx, span := StartTraceFromMessage(zap.NewNop(), context.Background(), &msg, tracerProvider.Tracer(""), "span-name")
	sc := trace.SpanContextFromContext(ctx)
	if traceId != sc.TraceID().String() {
		t.Fatalf("TraceId is incorrect, expected: %v, actual: %v", traceId, sc.TraceID())
	}
	if span == nil {
		t.Fatalf("Span must be non-nil")
	}
}

func TestStartTraceFromMessageIsNil(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	ctx, span := StartTraceFromMessage(zap.NewNop(), context.Background(), nil, tracerProvider.Tracer(""), "span-name")
	sc := trace.SpanContextFromContext(ctx)
	if traceId == sc.TraceID().String() {
		t.Fatalf("TraceId must be new")
	}
	if span == nil {
		t.Fatalf("Span must be non-nil")
	}
}

func TestStartTraceFromMessageTraceParentIsNil(t *testing.T) {
	msg := cloudevents.NewEvent()
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	ctx, span := StartTraceFromMessage(zap.NewNop(), context.Background(), &msg, tracerProvider.Tracer(""), "span-name")
	sc := trace.SpanContextFromContext(ctx)
	if traceId == sc.TraceID().String() {
		t.Fatalf("TraceId must be new")
	}
	if span == nil {
		t.Fatalf("Span must be non-nil")
	}
}

func TestStartTraceFromMessageTraceStateIsNil(t *testing.T) {
	msg := cloudevents.NewEvent()
	msg.SetExtension(traceParentHeader, tp)
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	ctx, span := StartTraceFromMessage(zap.NewNop(), context.Background(), &msg, tracerProvider.Tracer(""), "span-name")
	sc := trace.SpanContextFromContext(ctx)
	if traceId != sc.TraceID().String() {
		t.Fatalf("TraceId is incorrect, expected: %v, actual: %v", traceId, sc.TraceID())
	}
	if span == nil {
		t.Fatalf("Span must be non-nil")
	}
}

func TestSerializeTraceTransformers(t *testing.T) {
	const existingTraceParent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	const existingTraceState = "vendor=value"

	contextWithTrace := func(traceParent, traceState string) context.Context {
		headerCarrier := propagation.HeaderCarrier{}
		headerCarrier.Set(traceParentHeader, traceParent)
		if traceState != "" {
			headerCarrier.Set(traceStateHeader, traceState)
		}
		return format.Extract(context.Background(), headerCarrier)
	}

	tests := []struct {
		name                string
		ctx                 context.Context
		existingTraceParent interface{}
		existingTraceState  interface{}
		wantTraceParent     interface{}
		wantTraceState      interface{}
	}{
		{
			name:            "inject context when extension is absent",
			ctx:             contextWithTrace(tp, ts),
			wantTraceParent: tp,
			wantTraceState:  ts,
		},
		{
			name:                "preserve existing extension",
			ctx:                 contextWithTrace(tp, ts),
			existingTraceParent: existingTraceParent,
			existingTraceState:  existingTraceState,
			wantTraceParent:     existingTraceParent,
			wantTraceState:      existingTraceState,
		},
		{
			name:                "do not mix intermediary tracestate into existing traceparent",
			ctx:                 contextWithTrace(tp, ts),
			existingTraceParent: existingTraceParent,
			wantTraceParent:     existingTraceParent,
		},
		{
			name: "do not add empty extensions without context",
			ctx:  context.Background(),
		},
		{
			name:               "remove stale tracestate when injecting a new traceparent",
			ctx:                contextWithTrace(tp, ""),
			existingTraceState: existingTraceState,
			wantTraceParent:    tp,
		},
		{
			name:                "preserve existing extension without context",
			ctx:                 context.Background(),
			existingTraceParent: existingTraceParent,
			existingTraceState:  existingTraceState,
			wantTraceParent:     existingTraceParent,
			wantTraceState:      existingTraceState,
		},
		{
			name:                "preserve non-empty extension without parsing it",
			ctx:                 contextWithTrace(tp, ts),
			existingTraceParent: "invalid-but-owned-by-the-producer",
			existingTraceState:  existingTraceState,
			wantTraceParent:     "invalid-but-owned-by-the-producer",
			wantTraceState:      existingTraceState,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			msg := cloudevents.NewEvent()
			if test.existingTraceParent != nil {
				msg.SetExtension(traceParentHeader, test.existingTraceParent)
			}
			if test.existingTraceState != nil {
				msg.SetExtension(traceStateHeader, test.existingTraceState)
			}

			message := binding.ToMessage(&msg)
			event, err := binding.ToEvent(context.Background(), message, SerializeTraceTransformers(test.ctx)...)
			if err != nil {
				t.Fatalf("Failed to transform event: %v", err)
			}
			if got := event.Extensions()[traceParentHeader]; got != test.wantTraceParent {
				t.Fatalf("Traceparent is incorrect, expected: %v, actual: %v", test.wantTraceParent, got)
			}
			if got := event.Extensions()[traceStateHeader]; got != test.wantTraceState {
				t.Fatalf("Tracestate is incorrect, expected: %v, actual: %v", test.wantTraceState, got)
			}
		})
	}
}
