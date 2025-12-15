package main

import (
	"net/http"

	"github.com/cloudevents/sdk-go/v2/binding"
	"go.uber.org/zap"
	"knative.dev/eventing/pkg/kncloudevents"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/signals"

	cehttp "github.com/cloudevents/sdk-go/v2/protocol/http"
)

type ingress struct {
	logger *zap.SugaredLogger
}

func main() {
	ctx := signals.NewContext()
	loggingConfig, _ := logging.NewConfigFromMap(map[string]string{})
	logger, _ := logging.NewLoggerFromConfig(loggingConfig, "natsjs-ingress")
	i := ingress{
		logger: logger,
	}

	receiver := kncloudevents.NewHTTPEventReceiver(8080)
	if err := receiver.StartListen(ctx, &i); err != nil {
		logger.Fatalf("failed to start listen, %v", err)
	}
}

func (i *ingress) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	logger := i.logger
	logger.Info("serving http request")

	if request.Method != http.MethodPost {
		logger.Warn("unexpected request method", zap.String("method", request.Method))
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// validate request URI
	if request.RequestURI != "/" {
		logger.Error("unexpected incoming request uri")
		writer.WriteHeader(http.StatusNotFound)
		return
	}

	ctx := request.Context()
	message := cehttp.NewMessageFromHttpRequest(request)
	defer message.Finish(nil)

	event, err := binding.ToEvent(ctx, message)
	if err != nil {
		logger.Warnw("failed to extract event from request", zap.Error(err))
		writer.WriteHeader(http.StatusBadRequest)
		return
	}

	// run validation for the extracted event
	validationErr := event.Validate()
	if validationErr != nil {
		logger.Warnw("failed to validate extracted event", zap.Error(validationErr))
		writer.WriteHeader(http.StatusBadRequest)
		return
	}

	logger.Info("event received", zap.Any("event", event))

	writer.WriteHeader(http.StatusOK)
}
