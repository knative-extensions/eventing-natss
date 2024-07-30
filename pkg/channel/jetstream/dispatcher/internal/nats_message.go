package internal

import (
	"context"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"time"
)

type NatsMessageWrapper interface {
	Ack(ctx context.Context) error
	NakWithDelay(number time.Duration, ctx context.Context) error
	Term(ctx context.Context) error
	NumDelivered() (int, error)
}

// =======================

type natsMessageWrapperImpl struct {
	natsMsg *nats.Msg
}

func (n *natsMessageWrapperImpl) NumDelivered() (int, error) {
	meta, err := n.natsMsg.Metadata()
	if err != nil {
		return 0, err
	}
	return int(meta.NumDelivered), nil
}

func (n *natsMessageWrapperImpl) Ack(ctx context.Context) error {
	return n.natsMsg.Ack(nats.Context(ctx))
}

func (n *natsMessageWrapperImpl) NakWithDelay(delay time.Duration, ctx context.Context) error {
	return n.natsMsg.NakWithDelay(delay, nats.Context(ctx))
}

func (n *natsMessageWrapperImpl) Term(ctx context.Context) error {
	return n.natsMsg.Term(nats.Context(ctx))
}

// =======================

type jsMessageWrapperImpl struct {
	jsMsg jetstream.Msg
}

func (j *jsMessageWrapperImpl) NumDelivered() (int, error) {
	meta, err := j.jsMsg.Metadata()
	if err != nil {
		return 0, err
	}
	return int(meta.NumDelivered), nil
}

func (j *jsMessageWrapperImpl) Ack(_ context.Context) error {
	return j.jsMsg.Ack()
}

func (j *jsMessageWrapperImpl) NakWithDelay(delay time.Duration, _ context.Context) error {
	return j.jsMsg.NakWithDelay(delay)
}

func (j *jsMessageWrapperImpl) Term(_ context.Context) error {
	return j.jsMsg.Term()
}

func NewNatsMessageWrapper(natsMsg *nats.Msg) NatsMessageWrapper {
	return &natsMessageWrapperImpl{
		natsMsg: natsMsg,
	}
}

func NewJsMessageWrapper(jsMsg jetstream.Msg) NatsMessageWrapper {
	return &jsMessageWrapperImpl{
		jsMsg: jsMsg,
	}
}
