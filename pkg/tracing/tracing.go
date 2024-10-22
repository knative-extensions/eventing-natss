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
	"go.opencensus.io/plugin/ochttp/propagation/tracecontext"
	"go.opencensus.io/trace"
	"go.uber.org/zap"
)

const (
	traceParentHeader = "traceparent"
	traceStateHeader  = "tracestate"
)

var (
	format = tracecontext.HTTPFormat{}
)

func SerializeTraceTransformers(spanContext trace.SpanContext) []binding.Transformer {
	tp, ts := format.SpanContextToHeaders(spanContext)
	return []binding.Transformer{
		keyValTransformer(traceParentHeader, tp),
		keyValTransformer(traceStateHeader, ts),
	}
}

func keyValTransformer(key string, value string) binding.TransformerFunc {
	return transformer.SetExtension(key, func(_ interface{}) (interface{}, error) {
		return value, nil
	})
}

func StartTraceFromMessage(logger *zap.Logger, inCtx context.Context, message *event.Event, spanName string) (context.Context, *trace.Span) {
	sc, ok := ParseSpanContext(message)
	if !ok {
		logger.Warn("Cannot parse the spancontext, creating a new span")
		return trace.StartSpan(inCtx, spanName)
	}

	return trace.StartSpanWithRemoteParent(inCtx, spanName, sc)
}

func ParseSpanContext(message *event.Event) (sc trace.SpanContext, ok bool) {
	if message == nil {
		return trace.SpanContext{}, false
	}
	tp, ok := message.Extensions()[traceParentHeader].(string)
	if !ok {
		return trace.SpanContext{}, false
	}
	ts, _ := message.Extensions()[traceStateHeader].(string)

	return format.SpanContextFromHeaders(tp, ts)
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
