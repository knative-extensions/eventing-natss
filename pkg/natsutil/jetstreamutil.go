package natsutil

import (
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

const (
	//  StreamName is the name of StreamConfig for JetStream
	StreamName = "K-ORDERS"

	// MaxPending is the maximum outstanding async publishes that can be inflight at one time.
	MaxPending = 256
)

// JetStreamConnect creates a new NATS JetStream connection
func JetStreamConnect(jetStreamUrl string, logger *zap.SugaredLogger, natsUserOrChainedFile string, natsSeedFiles []string) (*nats.Conn, error) {
	logger.Infof("JetStreamConnect():  jetStreamUrl: %v", jetStreamUrl)

	var nc *nats.Conn
	var err error

	if natsUserOrChainedFile == "" || natsSeedFiles == nil || len(natsSeedFiles) == 0 {
		nc, err = nats.Connect(jetStreamUrl)
	} else {
		nc, err = nats.Connect(jetStreamUrl, nats.UserCredentials(natsUserOrChainedFile, natsSeedFiles...))
	}

	if err != nil {
		logger.Errorf("Connect(): create new connection failed: %v", err)
		return nil, err
	}
	logger.Infof("Connect(): connection to NATS JetStream established!")

	// Create JetStream Context
	js, err := nc.JetStream(nats.PublishAsyncMaxPending(MaxPending))
	if err != nil {
		logger.Errorf("Connect(): create JetStream connection failed: %v", err)
		return nil, err
	}

	streamConfig := nats.StreamConfig{
		Name:     StreamName,
		Subjects: []string{StreamName + ".*"},
	}

	_, err = js.AddStream(&streamConfig)
	if err != nil {
		logger.Errorf("Connect(): AddStream %#v failed: %v", streamConfig, err)
		return nil, err
	}
	return nc, nil
}
