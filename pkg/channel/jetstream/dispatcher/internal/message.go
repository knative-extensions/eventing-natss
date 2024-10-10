package internal

import (
	"bytes"
	"context"
	"time"

	"github.com/cloudevents/sdk-go/v2/binding/format"

	"github.com/cloudevents/sdk-go/v2/binding"
	"github.com/nats-io/nats.go"
)

type Message interface {
	binding.Message

	NatsMessage() *nats.Msg
	// Metadata returns [MsgMetadata] for a JetStream message
	Metadata() (*nats.MsgMetadata, error)
	// Data returns the message body
	Data() []byte
	// Headers returns a map of headers for a message
	Headers() nats.Header
	// Subject returns a subject on which a message is published
	Subject() string
	// Reply returns a reply subject for a message
	Reply() string

	Context() context.Context
}

type msgImpl struct {
	*nats.Msg
	ctx    context.Context
	finish context.CancelFunc
}

func (m *msgImpl) Headers() nats.Header {
	return m.Msg.Header
}

func (m *msgImpl) Subject() string {
	return m.Msg.Subject
}

func (m *msgImpl) Reply() string {
	return m.Msg.Reply
}

func (m *msgImpl) Data() []byte {
	return m.Msg.Data
}

func (m *msgImpl) ReadEncoding() binding.Encoding {
	return binding.EncodingStructured
}

func (m *msgImpl) ReadStructured(ctx context.Context, writer binding.StructuredWriter) error {
	return writer.SetStructuredEvent(ctx, format.JSON, bytes.NewReader(m.Data()))
}

func (m *msgImpl) ReadBinary(ctx context.Context, writer binding.BinaryWriter) error {
	return binding.ErrNotBinary
}

func (m *msgImpl) NatsMessage() *nats.Msg {
	return m.Msg
}

func (m *msgImpl) Context() context.Context {
	return m.ctx
}

func (m *msgImpl) Finish(_ error) error {
	m.finish()
	return nil
}

func NewMessage(ctx context.Context, msg *nats.Msg, ackWait time.Duration) Message {
	ctx, finish := context.WithTimeout(ctx, ackWait)

	return &msgImpl{
		Msg:    msg,
		ctx:    ctx,
		finish: finish,
	}
}
