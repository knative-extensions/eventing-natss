package tracing

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/nats-io/nats.go"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/cloudevents/sdk-go/v2/binding"
	"github.com/cloudevents/sdk-go/v2/binding/transformer"
	"github.com/cloudevents/sdk-go/v2/event"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

const (
	traceParentHeader = "traceparent"
	traceStateHeader  = "tracestate"
)

var (
	format = propagation.TraceContext{}
)

func SerializeTraceTransformers(ctx context.Context) []binding.Transformer {
	headerCarrier := propagation.HeaderCarrier{}
	format.Inject(ctx, headerCarrier)
	return []binding.Transformer{
		keyValTransformer(traceParentHeader, headerCarrier.Get(traceParentHeader)),
		keyValTransformer(traceStateHeader, headerCarrier.Get(traceStateHeader)),
	}
}

func keyValTransformer(key string, value string) binding.TransformerFunc {
	return transformer.SetExtension(key, func(_ interface{}) (interface{}, error) {
		return value, nil
	})
}

func StartTraceFromMessage(logger *zap.Logger, inCtx context.Context, message *event.Event, tracer trace.Tracer, spanName string) (context.Context, trace.Span) {
	ctx := ParseSpanContext(inCtx, message)
	return tracer.Start(ctx, spanName)
}

func ParseSpanContext(ctx context.Context, message *event.Event) context.Context {
	if message == nil {
		return ctx
	}
	tp, ok := message.Extensions()[traceParentHeader].(string)
	if !ok {
		return ctx
	}
	ts, _ := message.Extensions()[traceStateHeader].(string)

	headerCarrier := propagation.HeaderCarrier{}

	headerCarrier.Set(traceParentHeader, tp)
	headerCarrier.Set(traceStateHeader, ts)

	return format.Extract(ctx, headerCarrier)
}

func ConvertEventToHttpHeader(message *event.Event) http.Header {
	additionalHeaders := http.Header{}
	tp, ok := message.Extensions()[traceParentHeader].(string)
	if ok {
		additionalHeaders.Add(traceParentHeader, tp)
	}
	ts, ok := message.Extensions()[traceStateHeader].(string)
	if ok {
		additionalHeaders.Add(traceStateHeader, ts)
	}
	return additionalHeaders
}

func ConvertNatsMsgToEvent(logger *zap.Logger, msg *nats.Msg) *event.Event {
	message := cloudevents.NewEvent()
	if msg == nil || msg.Data == nil {
		return &message
	}
	err := json.Unmarshal(msg.Data, &message)
	if err != nil {
		logger.Error("could not create an event from nats msg", zap.Error(err))
		return &message
	}

	return &message
}

func ConvertJsMsgToEvent(logger *zap.Logger, msg jetstream.Msg) *event.Event {
	message := cloudevents.NewEvent()
	if msg == nil || msg.Data() == nil {
		return &message
	}
	err := json.Unmarshal(msg.Data(), &message)
	if err != nil {
		logger.Error("could not create an event from js msg", zap.Error(err))
		return &message
	}

	return &message
}
