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
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"net/url"
	"time"

	"github.com/cloudevents/sdk-go/v2/protocol"
	"github.com/nats-io/nats.go"
	"knative.dev/eventing/pkg/utils"
	"knative.dev/pkg/logging"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/cloudevents/sdk-go/v2/binding"
	"github.com/cloudevents/sdk-go/v2/protocol/http"
	"go.opencensus.io/trace"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/util/sets"

	jsutils "knative.dev/eventing-natss/pkg/channel/jetstream/utils"
	"knative.dev/eventing/pkg/broker"
	eventingchannels "knative.dev/eventing/pkg/channel"
	"knative.dev/eventing/pkg/channel/attributes"
	"knative.dev/eventing/pkg/kncloudevents"
	"knative.dev/eventing/pkg/tracing"
	"knative.dev/pkg/network"
	"knative.dev/pkg/system"
)

const (
	// noDuration signals that the dispatch step hasn't started
	NoDuration = -1
	NoResponse = -1
)

type NatsMessageDispatcher interface {
	DispatchMessageWithNatsRetries(ctx context.Context, message cloudevents.Message, additionalHeaders nethttp.Header, destination *url.URL, reply *url.URL, deadLetter *url.URL, config *kncloudevents.RetryConfig, ackWait time.Duration, msg *nats.Msg, transformers ...binding.Transformer) (*eventingchannels.DispatchExecutionInfo, error)
}

// MessageDispatcherImpl is the 'real' MessageDispatcher used everywhere except unit tests.
var _ NatsMessageDispatcher = &NatsMessageDispatcherImpl{}

type NatsMessageDispatcherImpl struct {
	sender           *kncloudevents.HTTPMessageSender
	supportedSchemes sets.String

	logger *zap.Logger
}

func NewNatsMessageDispatcher(logger *zap.Logger) *NatsMessageDispatcherImpl {
	sender, err := kncloudevents.NewHTTPMessageSenderWithTarget("")
	if err != nil {
		logger.Fatal("Unable to create cloudevents binding sender", zap.Error(err))
	}
	return NewMessageDispatcherFromSender(logger, sender)
}

func NewMessageDispatcherFromSender(logger *zap.Logger, sender *kncloudevents.HTTPMessageSender) *NatsMessageDispatcherImpl {
	return &NatsMessageDispatcherImpl{
		sender:           sender,
		supportedSchemes: sets.NewString("http", "https"),
		logger:           logger,
	}
}

func (d *NatsMessageDispatcherImpl) DispatchMessageWithNatsRetries(ctx context.Context, message cloudevents.Message, additionalHeaders nethttp.Header, destination *url.URL, reply *url.URL, deadLetter *url.URL, retriesConfig *kncloudevents.RetryConfig, ackWait time.Duration, msg *nats.Msg, transformers ...binding.Transformer) (*eventingchannels.DispatchExecutionInfo, error) {
	logger := logging.FromContext(ctx)
	// All messages that should be finished at the end of this function
	// are placed in this slice
	var messagesToFinish []binding.Message
	defer func() {
		for _, msg := range messagesToFinish {
			_ = msg.Finish(nil)
		}
	}()

	// sanitize eventual host-only URLs
	destination = d.sanitizeURL(destination)
	reply = d.sanitizeURL(reply)
	deadLetter = d.sanitizeURL(deadLetter)
	var noRetires = kncloudevents.NoRetries()
	var lastTry bool

	meta, err := msg.Metadata()
	retryNumber := 1
	if err != nil {
		logger.Errorw("failed to get nats message metadata, assuming it is 1", zap.Error(err))
	} else {
		retryNumber = int(meta.NumDelivered)
	}

	if retryNumber <= retriesConfig.RetryMax {
		lastTry = false
	} else {
		lastTry = true
	}

	// If there is a destination, variables response* are filled with the response of the destination
	// Otherwise, they are filled with the original message
	var responseMessage cloudevents.Message
	var responseAdditionalHeaders nethttp.Header
	var dispatchExecutionInfo *eventingchannels.DispatchExecutionInfo

	if destination != nil {
		var err error
		// Try to send to destination
		messagesToFinish = append(messagesToFinish, message)

		// Add `Prefer: reply` header no matter if a reply destination is provided. Discussion: https://github.com/knative/eventing/pull/5764
		additionalHeadersForDestination := nethttp.Header{}
		if additionalHeaders != nil {
			additionalHeadersForDestination = additionalHeaders.Clone()
		}
		additionalHeadersForDestination.Set("Prefer", "reply")

		noRetires.RequestTimeout = jsutils.CalcRequestTimeout(msg, ackWait)

		ctx, responseMessage, responseAdditionalHeaders, dispatchExecutionInfo, err = d.executeRequest(ctx, destination, message, additionalHeadersForDestination, &noRetires, transformers...)

		d.processDispatchResult(ctx, msg, retriesConfig, retryNumber, dispatchExecutionInfo, err)

		if err != nil && lastTry {
			// If DeadLetter is configured, then send original message with knative error extensions
			if deadLetter != nil {
				dispatchTransformers := d.dispatchExecutionInfoTransformers(destination, dispatchExecutionInfo)
				_, deadLetterResponse, _, dispatchExecutionInfo, deadLetterErr := d.executeRequest(ctx, deadLetter, message, additionalHeaders, retriesConfig, append(transformers, dispatchTransformers)...)
				if deadLetterErr != nil {
					return dispatchExecutionInfo, fmt.Errorf("unable to complete request to either %s (%v) or %s (%v)", destination, err, deadLetter, deadLetterErr)
				}
				if deadLetterResponse != nil {
					messagesToFinish = append(messagesToFinish, deadLetterResponse)
				}

				return dispatchExecutionInfo, nil
			}
			// No DeadLetter, just fail
			return dispatchExecutionInfo, fmt.Errorf("unable to complete request to %s: %v, last try failed", destination, err)
		} else if err != nil && !lastTry {
			return dispatchExecutionInfo, fmt.Errorf("unable to complete request to %s: %v, going for retry", destination, err)
		}
	} else {
		// No destination url, try to send to reply if available
		responseMessage = message
		responseAdditionalHeaders = additionalHeaders
	}

	// No response, dispatch completed
	if responseMessage == nil {
		return dispatchExecutionInfo, nil
	}

	messagesToFinish = append(messagesToFinish, responseMessage)

	if reply == nil {
		d.logger.Debug("cannot forward response as reply is empty")
		return dispatchExecutionInfo, nil
	}

	ctx, responseResponseMessage, _, dispatchExecutionInfo, err := d.executeRequest(ctx, reply, responseMessage, responseAdditionalHeaders, retriesConfig, transformers...)
	if err != nil {
		// If DeadLetter is configured, then send original message with knative error extensions
		if deadLetter != nil {
			dispatchTransformers := d.dispatchExecutionInfoTransformers(reply, dispatchExecutionInfo)
			_, deadLetterResponse, _, dispatchExecutionInfo, deadLetterErr := d.executeRequest(ctx, deadLetter, message, responseAdditionalHeaders, retriesConfig, append(transformers, dispatchTransformers)...)
			if deadLetterErr != nil {
				return dispatchExecutionInfo, fmt.Errorf("failed to forward reply to %s (%v) and failed to send it to the dead letter sink %s (%v)", reply, err, deadLetter, deadLetterErr)
			}
			if deadLetterResponse != nil {
				messagesToFinish = append(messagesToFinish, deadLetterResponse)
			}

			return dispatchExecutionInfo, nil
		}
		// No DeadLetter, just fail
		return dispatchExecutionInfo, fmt.Errorf("failed to forward reply to %s: %v", reply, err)
	}
	if responseResponseMessage != nil {
		messagesToFinish = append(messagesToFinish, responseResponseMessage)
	}

	return dispatchExecutionInfo, nil
}

func (d *NatsMessageDispatcherImpl) processDispatchResult(ctx context.Context, msg *nats.Msg, retryConfig *kncloudevents.RetryConfig, retryNumber int, dispatchExecutionInfo *eventingchannels.DispatchExecutionInfo, err error) {
	result := protocol.ResultACK

	if err != nil {
		d.logger.Error("failed to execute message",
			zap.Error(err),
			zap.Any("dispatch_resp_code", dispatchExecutionInfo.ResponseCode))

		code := dispatchExecutionInfo.ResponseCode
		if code/100 == 5 || code == nethttp.StatusTooManyRequests || code == nethttp.StatusRequestTimeout {
			// tell JSM to redeliver the message later
			result = protocol.NewReceipt(false, "%w", err)
		} else {
			result = err
		}
	}

	switch {
	case protocol.IsACK(result):
		if err := msg.Ack(nats.Context(ctx)); err != nil {
			d.logger.Error("failed to Ack message after successful delivery to subscriber", zap.Error(err))
		}
	case protocol.IsNACK(result):
		if err := msg.NakWithDelay(jsutils.CalculateNakDelayForRetryNumber(retryNumber, retryConfig), nats.Context(ctx)); err != nil {
			d.logger.Error("failed to Nack message after failed delivery to subscriber", zap.Error(err))
		}
	default:
		if err := msg.Term(nats.Context(ctx)); err != nil {
			d.logger.Error("failed to Term message after failed delivery to subscriber", zap.Error(err))
		}
	}
}

func (d *NatsMessageDispatcherImpl) executeRequest(ctx context.Context,
	url *url.URL,
	message cloudevents.Message,
	additionalHeaders nethttp.Header,
	configs *kncloudevents.RetryConfig,
	transformers ...binding.Transformer) (context.Context, cloudevents.Message, nethttp.Header, *eventingchannels.DispatchExecutionInfo, error) {

	d.logger.Debug("Dispatching event", zap.String("url", url.String()))

	execInfo := eventingchannels.DispatchExecutionInfo{
		Time:         NoDuration,
		ResponseCode: NoResponse,
	}
	ctx, span := trace.StartSpan(ctx, "knative.dev", trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()

	req, err := d.sender.NewCloudEventRequestWithTarget(ctx, url.String())
	if err != nil {
		return ctx, nil, nil, &execInfo, err
	}

	if span.IsRecordingEvents() {
		transformers = append(transformers, tracing.PopulateSpan(span, url.String()))
	}

	err = kncloudevents.WriteHTTPRequestWithAdditionalHeaders(ctx, message, req, additionalHeaders, transformers...)
	if err != nil {
		return ctx, nil, nil, &execInfo, err
	}

	start := time.Now()
	response, err := d.sender.SendWithRetries(req, configs)
	dispatchTime := time.Since(start)
	if err != nil {
		execInfo.Time = dispatchTime
		execInfo.ResponseCode = nethttp.StatusInternalServerError
		execInfo.ResponseBody = []byte(fmt.Sprintf("dispatch error: %s", err.Error()))
		return ctx, nil, nil, &execInfo, err
	}

	if response != nil {
		execInfo.ResponseCode = response.StatusCode
	}
	execInfo.Time = dispatchTime

	body := new(bytes.Buffer)
	_, readErr := body.ReadFrom(response.Body)

	if isFailure(response.StatusCode) {
		// Read response body into execInfo for failures
		if readErr != nil && readErr != io.EOF {
			d.logger.Error("failed to read response body", zap.Error(err))
			execInfo.ResponseBody = []byte(fmt.Sprintf("dispatch error: %s", err.Error()))
		} else {
			execInfo.ResponseBody = body.Bytes()
		}
		_ = response.Body.Close()
		// Reject non-successful responses.
		return ctx, nil, nil, &execInfo, fmt.Errorf("unexpected HTTP response, expected 2xx, got %d", response.StatusCode)
	}

	var responseMessageBody []byte
	if readErr != nil && readErr != io.EOF {
		d.logger.Error("failed to read response body", zap.Error(err))
		responseMessageBody = []byte(fmt.Sprintf("Failed to read response body: %s", err.Error()))
	} else {
		responseMessageBody = body.Bytes()
	}
	responseMessage := http.NewMessage(response.Header, io.NopCloser(bytes.NewReader(responseMessageBody)))

	if responseMessage.ReadEncoding() == binding.EncodingUnknown {
		_ = response.Body.Close()
		_ = responseMessage.BodyReader.Close()
		d.logger.Debug("Response is a non event, discarding it", zap.Int("status_code", response.StatusCode))
		return ctx, nil, nil, &execInfo, nil
	}
	return ctx, responseMessage, utils.PassThroughHeaders(response.Header), &execInfo, nil
}

func (d *NatsMessageDispatcherImpl) sanitizeURL(u *url.URL) *url.URL {
	if u == nil {
		return nil
	}
	if d.supportedSchemes.Has(u.Scheme) {
		// Already a URL with a known scheme.
		return u
	}
	return &url.URL{
		Scheme: "http",
		Host:   u.Host,
		Path:   "/",
	}
}

// dispatchExecutionTransformer returns Transformers based on the specified destination and DispatchExecutionInfo
func (d *NatsMessageDispatcherImpl) dispatchExecutionInfoTransformers(destination *url.URL, dispatchExecutionInfo *eventingchannels.DispatchExecutionInfo) binding.Transformers {
	if destination == nil {
		destination = &url.URL{}
	}

	httpResponseBody := dispatchExecutionInfo.ResponseBody
	if destination.Host == network.GetServiceHostname("broker-filter", system.Namespace()) {

		var errExtensionInfo broker.ErrExtensionInfo

		err := json.Unmarshal(dispatchExecutionInfo.ResponseBody, &errExtensionInfo)
		if err != nil {
			d.logger.Debug("Unmarshal dispatchExecutionInfo ResponseBody failed", zap.Error(err))
			return nil
		}
		destination = errExtensionInfo.ErrDestination
		httpResponseBody = errExtensionInfo.ErrResponseBody
	}

	destination = d.sanitizeURL(destination)
	// Unprintable control characters are not allowed in header values
	// and cause HTTP requests to fail if not removed.
	// https://pkg.go.dev/golang.org/x/net/http/httpguts#ValidHeaderFieldValue
	httpBody := sanitizeHTTPBody(httpResponseBody)

	// Encodes response body as base64 for the resulting length.
	bodyLen := len(httpBody)
	encodedLen := base64.StdEncoding.EncodedLen(bodyLen)
	if encodedLen > attributes.KnativeErrorDataExtensionMaxLength {
		encodedLen = attributes.KnativeErrorDataExtensionMaxLength
	}
	encodedBuf := make([]byte, encodedLen)
	base64.StdEncoding.Encode(encodedBuf, []byte(httpBody))

	return attributes.KnativeErrorTransformers(*destination, dispatchExecutionInfo.ResponseCode, string(encodedBuf[:encodedLen]))
}

func sanitizeHTTPBody(body []byte) string {
	if !hasControlChars(body) {
		return string(body)
	}

	sanitizedResponse := make([]byte, 0, len(body))
	for _, v := range body {
		if !isControl(v) {
			sanitizedResponse = append(sanitizedResponse, v)
		}
	}
	return string(sanitizedResponse)
}

func hasControlChars(data []byte) bool {
	for _, v := range data {
		if isControl(v) {
			return true
		}
	}
	return false
}

func isControl(c byte) bool {
	// US ASCII codes range for printable graphic characters and a space.
	// http://www.columbia.edu/kermit/ascii.html
	const asciiUnitSeparator = 31
	const asciiRubout = 127

	return int(c) < asciiUnitSeparator || int(c) > asciiRubout
}

// isFailure returns true if the status code is not a successful HTTP status.
func isFailure(statusCode int) bool {
	return statusCode < nethttp.StatusOK /* 200 */ ||
		statusCode >= nethttp.StatusMultipleChoices /* 300 */
}
