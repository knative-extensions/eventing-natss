/*
Copyright 2020 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dispatcher

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"knative.dev/pkg/apis"

	"github.com/cloudevents/sdk-go/v2/protocol"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
	"knative.dev/eventing/pkg/kncloudevents"
	"knative.dev/pkg/logging"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/cloudevents/sdk-go/v2/binding"
	"github.com/hashicorp/go-retryablehttp"
	"k8s.io/apimachinery/pkg/types"
	jsutils "knative.dev/eventing-natss/pkg/channel/jetstream/utils"

	eventingapis "knative.dev/eventing/pkg/apis"
	"knative.dev/eventing/pkg/utils"
	duckv1 "knative.dev/pkg/apis/duck/v1"

	_ "unsafe"
)

//go:linkname getClientForAddressable knative.dev/eventing/pkg/kncloudevents.getClientForAddressable
func getClientForAddressable(addressable duckv1.Addressable) (*http.Client, error)

//go:linkname generateBackoffFn knative.dev/eventing/pkg/kncloudevents.generateBackoffFn
func generateBackoffFn(config *kncloudevents.RetryConfig) retryablehttp.Backoff

//go:linkname sanitizeAddressable knative.dev/eventing/pkg/kncloudevents.sanitizeAddressable
func sanitizeAddressable(addressable *duckv1.Addressable) *duckv1.Addressable

//go:linkname dispatchExecutionInfoTransformers knative.dev/eventing/pkg/kncloudevents.dispatchExecutionInfoTransformers
func dispatchExecutionInfoTransformers(destination *apis.URL, dispatchExecutionInfo *kncloudevents.DispatchInfo) binding.Transformers

//go:linkname executeRequest knative.dev/eventing/pkg/kncloudevents.(*Dispatcher).executeRequest
func executeRequest(d *kncloudevents.Dispatcher, ctx context.Context, target duckv1.Addressable, message cloudevents.Message, additionalHeaders http.Header, retryConfig *kncloudevents.RetryConfig, oidcServiceAccount *types.NamespacedName, transformers ...binding.Transformer) (context.Context, cloudevents.Message, *kncloudevents.DispatchInfo, error)

type SendOption func(*senderConfig) error

func WithReply(reply *duckv1.Addressable) SendOption {
	return func(sc *senderConfig) error {
		sc.reply = reply

		return nil
	}
}

func WithDeadLetterSink(dls *duckv1.Addressable) SendOption {
	return func(sc *senderConfig) error {
		sc.deadLetterSink = dls

		return nil
	}
}

func WithRetryConfig(retryConfig *kncloudevents.RetryConfig) SendOption {
	return func(sc *senderConfig) error {
		sc.retryConfig = retryConfig

		return nil
	}
}

func WithHeader(header http.Header) SendOption {
	return func(sc *senderConfig) error {
		sc.additionalHeaders = header

		return nil
	}
}

func WithTransformers(transformers ...binding.Transformer) SendOption {
	return func(sc *senderConfig) error {
		sc.transformers = transformers

		return nil
	}
}

type senderConfig struct {
	reply              *duckv1.Addressable
	deadLetterSink     *duckv1.Addressable
	additionalHeaders  http.Header
	retryConfig        *kncloudevents.RetryConfig
	transformers       binding.Transformers
	oidcServiceAccount *types.NamespacedName
}

func SendMessage(dispatcher *kncloudevents.Dispatcher, ctx context.Context, message binding.Message, destination duckv1.Addressable, ackWait time.Duration, msg *nats.Msg, options ...SendOption) (*kncloudevents.DispatchInfo, error) {
	config := &senderConfig{
		additionalHeaders: make(http.Header),
	}

	// apply options
	for _, opt := range options {
		if err := opt(config); err != nil {
			return nil, fmt.Errorf("could not apply option: %w", err)
		}
	}

	return send(dispatcher, ctx, message, destination, ackWait, msg, config)
}

func send(dispatcher *kncloudevents.Dispatcher, ctx context.Context, message binding.Message, destination duckv1.Addressable, ackWait time.Duration, msg *nats.Msg, config *senderConfig) (*kncloudevents.DispatchInfo, error) {
	logger := logging.FromContext(ctx)
	dispatchExecutionInfo := &kncloudevents.DispatchInfo{}

	// All messages that should be finished at the end of this function
	// are placed in this slice
	messagesToFinish := []binding.Message{message}
	defer func() {
		for _, msg := range messagesToFinish {
			_ = msg.Finish(nil)
		}
	}()

	if destination.URL == nil {
		return dispatchExecutionInfo, fmt.Errorf("can not dispatch message to nil destination.URL")
	}

	// sanitize eventual host-only URLs
	destination = *sanitizeAddressable(&destination)
	config.reply = sanitizeAddressable(config.reply)
	config.deadLetterSink = sanitizeAddressable(config.deadLetterSink)

	var noRetires = kncloudevents.NoRetries()
	var lastTry bool

	meta, err := msg.Metadata()
	retryNumber := 1
	if err != nil {
		logger.Errorw("failed to get nats message metadata, assuming it is 1", zap.Error(err))
	} else {
		retryNumber = int(meta.NumDelivered)
	}

	if retryNumber <= config.retryConfig.RetryMax {
		lastTry = false
	} else {
		lastTry = true
	}

	// send to destination

	// Add `Prefer: reply` header no matter if a reply destination is provided. Discussion: https://github.com/knative/eventing/pull/5764
	additionalHeadersForDestination := http.Header{}
	if config.additionalHeaders != nil {
		additionalHeadersForDestination = config.additionalHeaders.Clone()
	}
	additionalHeadersForDestination.Set("Prefer", "reply")

	noRetires.RequestTimeout = jsutils.CalcRequestTimeout(msg, ackWait)

	ctx, responseMessage, dispatchExecutionInfo, err := executeRequest(dispatcher, ctx, destination, message, additionalHeadersForDestination, &noRetires, config.oidcServiceAccount, config.transformers)
	processDispatchResult(ctx, msg, config.retryConfig, retryNumber, dispatchExecutionInfo, err)

	if err != nil && lastTry {
		// If DeadLetter is configured, then send original message with knative error extensions
		if config.deadLetterSink != nil {
			dispatchTransformers := dispatchExecutionInfoTransformers(destination.URL, dispatchExecutionInfo)
			_, deadLetterResponse, dispatchExecutionInfo, deadLetterErr := executeRequest(dispatcher, ctx, *config.deadLetterSink, message, config.additionalHeaders, config.retryConfig, config.oidcServiceAccount, append(config.transformers, dispatchTransformers))
			if deadLetterErr != nil {
				return dispatchExecutionInfo, fmt.Errorf("unable to complete request to either %s (%v) or %s (%v)", destination.URL, err, config.deadLetterSink.URL, deadLetterErr)
			}
			if deadLetterResponse != nil {
				messagesToFinish = append(messagesToFinish, deadLetterResponse)
			}

			return dispatchExecutionInfo, nil
		}
		// No DeadLetter, just fail
		return dispatchExecutionInfo, fmt.Errorf("unable to complete request to %s: %w, last try failed", destination.URL, err)
	} else if err != nil && !lastTry {
		return dispatchExecutionInfo, fmt.Errorf("unable to complete request to %s: %w, going for retry", destination.URL, err)
	}

	responseAdditionalHeaders := utils.PassThroughHeaders(dispatchExecutionInfo.ResponseHeader)

	if config.additionalHeaders.Get(eventingapis.KnNamespaceHeader) != "" {
		if responseAdditionalHeaders == nil {
			responseAdditionalHeaders = make(http.Header)
		}
		responseAdditionalHeaders.Set(eventingapis.KnNamespaceHeader, config.additionalHeaders.Get(eventingapis.KnNamespaceHeader))
	}

	if responseMessage == nil {
		// No response, dispatch completed
		return dispatchExecutionInfo, nil
	}

	messagesToFinish = append(messagesToFinish, responseMessage)

	if config.reply == nil {
		return dispatchExecutionInfo, nil
	}

	// send reply

	ctx, responseResponseMessage, dispatchExecutionInfo, err := executeRequest(dispatcher, ctx, *config.reply, responseMessage, responseAdditionalHeaders, config.retryConfig, config.oidcServiceAccount, config.transformers)
	if err != nil {
		// If DeadLetter is configured, then send original message with knative error extensions
		if config.deadLetterSink != nil {
			dispatchTransformers := dispatchExecutionInfoTransformers(config.reply.URL, dispatchExecutionInfo)
			_, deadLetterResponse, dispatchExecutionInfo, deadLetterErr := executeRequest(dispatcher, ctx, *config.deadLetterSink, message, responseAdditionalHeaders, config.retryConfig, config.oidcServiceAccount, append(config.transformers, dispatchTransformers))
			if deadLetterErr != nil {
				return dispatchExecutionInfo, fmt.Errorf("failed to forward reply to %s (%v) and failed to send it to the dead letter sink %s (%v)", config.reply.URL, err, config.deadLetterSink.URL, deadLetterErr)
			}
			if deadLetterResponse != nil {
				messagesToFinish = append(messagesToFinish, deadLetterResponse)
			}

			return dispatchExecutionInfo, nil
		}
		// No DeadLetter, just fail
		return dispatchExecutionInfo, fmt.Errorf("failed to forward reply to %s: %w", config.reply.URL, err)
	}
	if responseResponseMessage != nil {
		messagesToFinish = append(messagesToFinish, responseResponseMessage)
	}

	return dispatchExecutionInfo, nil
}

func processDispatchResult(ctx context.Context, msg *nats.Msg, retryConfig *kncloudevents.RetryConfig, retryNumber int, dispatchExecutionInfo *kncloudevents.DispatchInfo, err error) {
	logger := logging.FromContext(ctx)
	result := protocol.ResultACK

	if err != nil {
		logger.Error("failed to execute message",
			zap.Error(err),
			zap.Any("dispatch_resp_code", dispatchExecutionInfo.ResponseCode))

		code := dispatchExecutionInfo.ResponseCode
		if code/100 == 5 || code == http.StatusTooManyRequests || code == http.StatusRequestTimeout {
			// tell JSM to redeliver the message later
			result = protocol.NewReceipt(false, "%w", err)
		} else {
			result = err
		}
	}

	switch {
	case protocol.IsACK(result):
		if err := msg.Ack(nats.Context(ctx)); err != nil {
			logger.Error("failed to Ack message after successful delivery to subscriber", zap.Error(err))
		}
	case protocol.IsNACK(result):
		if err := msg.NakWithDelay(jsutils.CalculateNakDelayForRetryNumber(retryNumber, retryConfig), nats.Context(ctx)); err != nil {
			logger.Error("failed to Nack message after failed delivery to subscriber", zap.Error(err))
		}
	default:
		if err := msg.Term(nats.Context(ctx)); err != nil {
			logger.Error("failed to Term message after failed delivery to subscriber", zap.Error(err))
		}
	}
}
