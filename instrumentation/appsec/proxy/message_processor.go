// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/actions"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/body/json"
)

// Processor is a state machine that handles incoming HTTP request and response is a streaming manner
// made for proxy external processing streaming protocols like Envoy's External Processing or HAProxy's SPOP
type Processor struct {
	ProcessorConfig
	instr *instrumentation.Instrumentation

	metrics      *metrics
	done         context.CancelFunc
	firstRequest sync.Once
}

// NewProcessor creates a new [Processor] instance with the given configuration and instrumentation
// It also initializes the metrics reporter and a context cancellation function
func NewProcessor(config ProcessorConfig, instr *instrumentation.Instrumentation) Processor {
	if config.BodyParsingSizeLimit <= 0 {
		instr.Logger().Info("external_processing: body parsing size limit set to 0 or negative. The request and response bodies will NOT be analyzed.")
	}

	if config.Context == nil {
		config.Context = context.Background()
	}
	var done context.CancelFunc
	config.Context, done = context.WithCancel(config.Context)
	return Processor{
		ProcessorConfig: config,
		instr:           instr,
		metrics:         newMetricsReporter(config.Context, instr.Logger()),
		done:            done,
	}
}

// OnRequestHeaders handles incoming request headers using the [RequestHeaders] interface
// It returns a [RequestState] to be used in subsequent calls for the same request/response cycle
// along with an optional output message of type O created by either [ProcessorConfig.ContinueMessageFunc] or [ProcessorConfig.BlockMessageFunc]
// If the request is blocked or the message ends the stream, it returns io.EOF as error
func (mp *Processor) OnRequestHeaders(ctx context.Context, req RequestHeaders) (reqState RequestState, err error) {
	mp.firstRequest.Do(func() {
		mp.instr.Logger().Info("external_processing: first request received. Configuration: BlockingUnavailable=%v, BodyParsingSizeLimit=%dB, Framework=%s", mp.BlockingUnavailable, mp.BodyParsingSizeLimit, mp.Framework)
	})

	mp.metrics.incrementRequestCount()
	pseudoRequest, err := req.ExtractRequest(ctx)
	if err != nil {
		return reqState, fmt.Errorf("error extracting request header from input message: %w", err)
	}

	httpRequest, err := pseudoRequest.toNetHTTP(ctx)
	if err != nil {
		return reqState, fmt.Errorf("error converting to net/http request: %w", err)
	}

	reqState, blocked := newRequestState(
		httpRequest,
		mp.BodyParsingSizeLimit,
		mp.Framework,
		req.SpanOptions(ctx)...,
	)

	defer func() {
		if err != nil {
			reqState.Close()
		}
	}()

	if !mp.BlockingUnavailable && blocked {
		actionOpts := reqState.BlockAction()
		if err := mp.BlockMessageFunc(reqState.Context, actionOpts); err != nil {
			return reqState, fmt.Errorf("error creating block message: %w", err)
		}
		return reqState, io.EOF
	}

	headerMutation, err := reqState.PropagationHeaders()
	if err != nil {
		return reqState, err
	}

	if !req.GetEndOfStream() && mp.isBodySupported(httpRequest.Header.Get("Content-Type")) {
		reqState.State = MessageTypeRequestBody
		// Todo: Set telemetry body size (using content-length)
	}

	if err := mp.ContinueMessageFunc(reqState.Context, ContinueActionOptions{
		HeaderMutations: headerMutation,
		Body:            reqState.State == MessageTypeRequestBody,
		MessageType:     MessageTypeRequestHeaders,
	}); err != nil {
		return reqState, fmt.Errorf("error creating continue message: %w", err)
	}

	return reqState, nil
}

// OnRequestBody handles incoming request body chunks using the [HTTPBody] interface
// It uses the provided [RequestState] to keep track of the request/response cycle state
// It returns an optional output message of type O created by either [ProcessorConfig.ContinueMessageFunc] or [ProcessorConfig.BlockMessageFunc]
// If the request is blocked or the message ends the stream, it returns io.EOF as error
// Once the whole body has been received, it will try to parse it following the Content-Type header
// and if the body is not too large, it will be analyzed by the WAF
func (mp *Processor) OnRequestBody(req HTTPBody, reqState *RequestState) error {
	if !reqState.State.Ongoing() {
		return errors.New("received request body too early")
	}

	mp.instr.Logger().Debug("message_processor: received request body: %v - EOS: %v\n", len(req.GetBody()), req.GetEndOfStream())

	if mp.BodyParsingSizeLimit <= 0 || reqState.State != MessageTypeRequestBody {
		mp.instr.Logger().Error("message_processor: the body parsing has been wrongly configured. " +
			"Please refer to the official documentation for guidance on the proper settings or contact support.")

		return mp.ContinueMessageFunc(reqState.Context, ContinueActionOptions{MessageType: MessageTypeRequestBody})
	}

	blocked := processBody(reqState.Context, reqState.requestBuffer, req.GetBody(), req.GetEndOfStream(), appsec.MonitorParsedHTTPBody)
	if blocked != nil && !mp.BlockingUnavailable {
		mp.instr.Logger().Debug("external_processing: request blocked, end the stream")
		actionOpts := reqState.BlockAction()
		if err := mp.BlockMessageFunc(reqState.Context, actionOpts); err != nil {
			return fmt.Errorf("error creating block message: %w", err)
		}
		return io.EOF
	}

	return mp.ContinueMessageFunc(reqState.Context, ContinueActionOptions{MessageType: MessageTypeRequestBody})
}

// OnResponseHeaders handles incoming response headers using the [ResponseHeaders] interface
// It returns a [RequestState] to be used in subsequent calls for the same request/response cycle
// along with an optional output message of type O created by either [ProcessorConfig.ContinueMessageFunc] or [ProcessorConfig.BlockMessageFunc]
// If the request is blocked or the message ends the stream, it returns io.EOF as error
func (mp *Processor) OnResponseHeaders(res ResponseHeaders, reqState *RequestState) error {
	if !reqState.State.Request() {
		return fmt.Errorf("received response headers too early: %v", reqState.State)
	}

	reqState.State = MessageTypeResponseHeaders

	pseudoResponse, err := res.ExtractResponse()
	if err != nil {
		return fmt.Errorf("error extracting response header from input message: %w", err)
	}

	pseudoResponse.toNetHTTP(reqState.wrappedResponseWriter)

	// We need to know if the request has been blocked, but we don't have any other way than to look for the operation and bind a blocking data listener to it
	if !mp.BlockingUnavailable {
		op, ok := dyngo.FromContext(reqState.Context)
		if ok {
			dyngo.OnData(op, func(_ *actions.BlockHTTP) {
				// We already wrote over the response writer, we need to reset it so the blocking Processor can write to it
				httptrace.ResetStatusCode(reqState.wrappedResponseWriter)
				reqState.fakeResponseWriter.Reset()
				reqState.State = MessageTypeBlocked
			})
		}
	}

	// TODO: Set telemetry body size (using content-length)
	reqState.State = MessageTypeResponseBody

	// Run the waf on the response headers only when we are sure to not receive a response body
	if res.GetEndOfStream() || !mp.isBodySupported(reqState.wrappedResponseWriter.Header().Get("Content-Type")) {
		reqState.Close()
		if !mp.BlockingUnavailable && reqState.State == MessageTypeBlocked {
			if err := mp.BlockMessageFunc(reqState.Context, reqState.BlockAction()); err != nil {
				return fmt.Errorf("error creating block message: %w", err)
			}
			return io.EOF
		}

		mp.instr.Logger().Debug("message_processor: finishing request with status code: %v\n", reqState.fakeResponseWriter.status)
		return io.EOF
	}

	return mp.ContinueMessageFunc(reqState.Context, ContinueActionOptions{MessageType: MessageTypeResponseHeaders, Body: reqState.State == MessageTypeResponseBody})
}

// OnResponseBody handles incoming response body chunks using the [HTTPBody] interface
// It uses the provided [RequestState] to keep track of the request/response cycle state
// It returns an optional output message of type O created by either [ProcessorConfig.ContinueMessageFunc] or [ProcessorConfig.BlockMessageFunc]
// If the request is blocked or the message ends the stream, it returns io.EOF as error
// Once the whole body has been received, it will try to parse it following the Content-Type header
// and if the body is not too large, it will be analyzed by the WAF
func (mp *Processor) OnResponseBody(resp HTTPBody, reqState *RequestState) error {
	if !reqState.State.Response() {
		return fmt.Errorf("received response body too early: %v", reqState.State)
	}

	mp.instr.Logger().Debug("message_processor: received response body: %v - EOS: %v\n", len(resp.GetBody()), resp.GetEndOfStream())

	if mp.BodyParsingSizeLimit <= 0 || reqState.State != MessageTypeResponseBody {
		mp.instr.Logger().Error("message_processor: the body parsing has been wrongly configured. " +
			"Please refer to the official documentation for guidance on the proper settings or contact support.")
		return io.EOF
	}

	blocked := processBody(reqState.Context, reqState.responseBuffer, resp.GetBody(), resp.GetEndOfStream(), appsec.MonitorHTTPResponseBody)
	if (blocked != nil && !mp.BlockingUnavailable) || resp.GetEndOfStream() || reqState.responseBuffer.truncated {
		blockOpts := reqState.BlockAction()
		mp.instr.Logger().Debug("external_processing: request blocked, end the stream")
		if err := mp.BlockMessageFunc(reqState.Context, blockOpts); err != nil {
			return fmt.Errorf("error creating block message: %w", err)
		}
		return io.EOF
	}

	if resp.GetEndOfStream() {
		return io.EOF
	}

	return mp.ContinueMessageFunc(reqState.Context, ContinueActionOptions{MessageType: MessageTypeResponseBody})
}

// OnRequestTrailers handles incoming request trailers
func (mp *Processor) OnRequestTrailers(reqState *RequestState) error {
	if reqState == nil {
		return fmt.Errorf("received a request trailer without a valid request state")
	}
	mp.instr.Logger().Debug("message_processor: received request trailers, ignoring")
	return mp.ContinueMessageFunc(reqState.Context, ContinueActionOptions{MessageType: MessageTypeRequestTrailers})
}

// OnResponseTrailers handles incoming response trailers
func (mp *Processor) OnResponseTrailers(reqState *RequestState) error {
	if reqState == nil {
		return fmt.Errorf("received a response trailer without a valid request state")
	}
	mp.instr.Logger().Debug("message_processor: received response trailers, ignoring")
	return mp.ContinueMessageFunc(reqState.Context, ContinueActionOptions{MessageType: MessageTypeResponseTrailers})
}

func processBody(ctx context.Context, bodyBuffer *bodyBuffer, body []byte, eos bool, analyzeBody func(ctx context.Context, encodable any) error) error {
	bodyBuffer.append(body)

	if eos || bodyBuffer.truncated {
		return analyzeBody(ctx, json.NewEncodableFromData(bodyBuffer.buffer, bodyBuffer.truncated))
	}

	return nil
}

// isBodySupported checks if the body should be analyzed based on content type
func (mp *Processor) isBodySupported(contentType string) bool {
	if mp.BodyParsingSizeLimit <= 0 {
		return false
	}

	parsedCT, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mp.instr.Logger().Debug("message_processor: error parsing content type '%s': %v", contentType, err)
		return false
	}

	// Handle cases like:
	// * application/json: https://www.iana.org/assignments/media-types/application/json
	// * application/vnd.api+json: https://jsonapi.org/
	// * text/json: https://mimetype.io/text/json
	return strings.HasSuffix(parsedCT, "json")
}

func (mp *Processor) Close() error {
	mp.done()
	return nil
}
