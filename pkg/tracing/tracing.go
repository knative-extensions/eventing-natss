package tracing

import (
	"context"
	"encoding/json"
	"net/http"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/cloudevents/sdk-go/v2/binding"
	"github.com/cloudevents/sdk-go/v2/binding/transformer"
	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/nats-io/stan.go"
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
	sc, ok := parseSpanContext(message)
	if !ok {
		logger.Warn("Cannot parse the spancontext, creating a new span")
		return trace.StartSpan(inCtx, spanName)
	}

	return trace.StartSpanWithRemoteParent(inCtx, spanName, sc)
}

func parseSpanContext(message *event.Event) (sc trace.SpanContext, ok bool) {
	if message == nil {
		return trace.SpanContext{}, false
	}
	tp, ok := message.Extensions()[traceParentHeader].(string)
	if !ok {
		return trace.SpanContext{}, false
	}
	ts := message.Extensions()[traceStateHeader].(string)

	return format.SpanContextFromHeaders(tp, ts)
}

func ConvertEventToHttpHeader(message *event.Event) http.Header {
	additionalHeaders := http.Header{}
	if message == nil {
		return additionalHeaders
	}
	tp := message.Extensions()[traceParentHeader].(string)
	ts := message.Extensions()[traceStateHeader].(string)
	additionalHeaders.Add(traceParentHeader, tp)
	additionalHeaders.Add(traceStateHeader, ts)

	return additionalHeaders
}

func ConvertNatssMsgToEvent(logger *zap.Logger, stanMsg *stan.Msg) *event.Event {
	message := cloudevents.NewEvent()
	err := json.Unmarshal(stanMsg.Data, &message)
	if err != nil {
		logger.Error("could not create a event from stan msg", zap.Error(err))
		return nil
	}

	return &message
}
