package tracing

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/nats-io/nats.go"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/cloudevents/sdk-go/v2/binding"
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

// SerializeTraceTransformers preserves an existing CloudEvents tracing extension,
// or injects the supplied context when the event does not already have one.
func SerializeTraceTransformers(ctx context.Context) []binding.Transformer {
	headerCarrier := propagation.HeaderCarrier{}
	format.Inject(ctx, headerCarrier)

	return []binding.Transformer{binding.TransformerFunc(func(reader binding.MessageMetadataReader, writer binding.MessageMetadataWriter) error {
		// The CloudEvents tracing extension represents the starting trace of a
		// multi-hop transmission. Intermediaries must not replace it with a hop context.
		if traceParent, ok := reader.GetExtension(traceParentHeader).(string); ok && traceParent != "" {
			return nil
		}

		traceParent := headerCarrier.Get(traceParentHeader)
		if traceParent == "" {
			return nil
		}

		if err := writer.SetExtension(traceParentHeader, traceParent); err != nil {
			return err
		}

		traceState := headerCarrier.Get(traceStateHeader)
		if traceState == "" {
			return writer.SetExtension(traceStateHeader, nil)
		}
		return writer.SetExtension(traceStateHeader, traceState)
	})}
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
