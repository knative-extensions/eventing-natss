package tracing

import (
	"testing"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/stan.go"
	"go.uber.org/zap"
)

const data = `{"specversion":"1.0","type":"type","source":"source","id":"1234-1234-1234","data":{"firstName":"John"}}`

func TestConvertEventToHttpHeader(t *testing.T) {
	tp := "00-8abe1a4854a9864ffa63046ef07b5dbe-8829876d85d5a76d-01"
	ts := "rojo=00f067aa0ba902b7"

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

func TestConvertNatssMsgToEventIsNotNullableIfNil(t *testing.T) {
	message := ConvertNatssMsgToEvent(zap.NewNop(), nil)
	if message == nil {
		t.Fatalf("Message must be non-nil")
	}
}

func TestConvertNatssMsgToEventIsNotNullableEmptyData(t *testing.T) {
	msg := stan.Msg{}
	msg.Data = []byte("{}")
	message := ConvertNatssMsgToEvent(zap.NewNop(), &msg)
	if message == nil {
		t.Fatalf("Message must be non-nil")
	}
}

func TestConvertNatssMsgToEventIsNotNullableData(t *testing.T) {
	msg := stan.Msg{}
	msg.Data = []byte(data)
	message := ConvertNatssMsgToEvent(zap.NewNop(), &msg)
	if message == nil {
		t.Fatalf("Message must be non-nil")
	}
}
