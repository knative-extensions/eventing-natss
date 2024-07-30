package internal

import (
	"bytes"
	"context"
	"time"

	"github.com/cloudevents/sdk-go/v2/binding"
	"github.com/cloudevents/sdk-go/v2/binding/format"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type Message interface {
	binding.Message

	NatsMessage() jetstream.Msg
	// Metadata returns [MsgMetadata] for a JetStream message
	Metadata() (*jetstream.MsgMetadata, error)
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
	jetstream.Msg
	ctx    context.Context
	finish context.CancelFunc
}

func (m *msgImpl) NatsMessage() jetstream.Msg {
	return m.Msg
}

func NewMessage(ctx context.Context, msg jetstream.Msg, ackWait time.Duration) Message {
	ctx, finish := context.WithTimeout(ctx, ackWait)

	return &msgImpl{
		Msg:    msg,
		ctx:    ctx,
		finish: finish,
	}
}

func (m *msgImpl) Context() context.Context {
	return m.ctx
}

func (m *msgImpl) ReadEncoding() binding.Encoding {
	return binding.EncodingStructured
}

func (m *msgImpl) ReadStructured(ctx context.Context, writer binding.StructuredWriter) error {
	return writer.SetStructuredEvent(ctx, format.JSON, bytes.NewReader(m.Msg.Data()))
}

func (m *msgImpl) ReadBinary(context.Context, binding.BinaryWriter) error {
	return binding.ErrNotBinary
}

func (m *msgImpl) Finish(_ error) error {
	m.finish()
	return nil
}
