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

	"github.com/DataDog/dd-trace-go/contrib/envoyproxy/go-control-plane/v2/internal/json"
	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/actions"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

// Processor is a state machine that handles incoming HTTP request and response is a streaming manner
// made for proxy external processing streaming protocols like Envoy's External Processing or HAProxy's SPOP
type Processor[O any] struct {
	ProcessorConfig[O]
	instr *instrumentation.Instrumentation

	metrics *metrics
	done    context.CancelFunc
}

// NewProcessor creates a new [Processor] instance with the given configuration and instrumentation
// It also initializes the metrics reporter and a context cancellation function
func NewProcessor[O any](config ProcessorConfig[O], instr *instrumentation.Instrumentation) Processor[O] {
	if config.BodyParsingSizeLimit <= 0 {
		instr.Logger().Info("external_processing: body parsing size limit set to 0 or negative. The request and response bodies will NOT be analyzed.")
	}

	if config.Context == nil {
		config.Context = context.Background()
	}
	var done context.CancelFunc
	config.Context, done = context.WithCancel(config.Context)
	return Processor[O]{
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
func (mp *Processor[O]) OnRequestHeaders(ctx context.Context, req RequestHeaders) (reqState RequestState, _ *O, err error) {
	mp.metrics.incrementRequestCount()
	pseudoRequest, err := req.ExtractRequest(ctx)
	if err != nil {
		return reqState, nil, fmt.Errorf("error extracting request header from input message: %w", err)
	}

	httpRequest, err := pseudoRequest.toNetHTTP(ctx)
	if err != nil {
		return reqState, nil, fmt.Errorf("error converting to net/http request: %w", err)
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
		outputMsg, err := mp.BlockMessageFunc(actionOpts)
		if err != nil {
			return reqState, nil, fmt.Errorf("error creating block message: %w", err)
		}
		return reqState, &outputMsg, io.EOF
	}

	headerMutation, err := reqState.PropagationHeaders()
	if err != nil {
		return reqState, nil, err
	}

	if !req.GetEndOfStream() && mp.isBodySupported(httpRequest.Header.Get("Content-Type")) {
		reqState.State = MessageTypeRequestBody
		// Todo: Set telemetry body size (using content-length)
	}

	action, err := mp.ContinueMessageFunc(ContinueActionOptions{
		HeaderMutations: headerMutation,
		Body:            reqState.State == MessageTypeRequestBody,
		MessageType:     MessageTypeRequestHeaders,
	})

	if err != nil {
		return reqState, nil, fmt.Errorf("error creating continue message: %w", err)
	}

	return reqState, &action, nil
}

// OnRequestBody handles incoming request body chunks using the [HTTPBody] interface
// It uses the provided [RequestState] to keep track of the request/response cycle state
// It returns an optional output message of type O created by either [ProcessorConfig.ContinueMessageFunc] or [ProcessorConfig.BlockMessageFunc]
// If the request is blocked or the message ends the stream, it returns io.EOF as error
// Once the whole body has been received, it will try to parse it following the Content-Type header
// and if the body is not too large, it will be analyzed by the WAF
func (mp *Processor[O]) OnRequestBody(req HTTPBody, reqState *RequestState) (*O, error) {
	if !reqState.State.Ongoing() {
		return nil, errors.New("received request body too early")
	}

	mp.instr.Logger().Debug("message_processor: received request body: %v - EOS: %v\n", len(req.GetBody()), req.GetEndOfStream())

	action, err := mp.ContinueMessageFunc(ContinueActionOptions{MessageType: MessageTypeRequestBody})
	if err != nil {
		return nil, fmt.Errorf("error creating continue message: %w", err)
	}

	if mp.BodyParsingSizeLimit <= 0 || reqState.State != MessageTypeRequestBody {
		mp.instr.Logger().Error("message_processor: the body parsing has been wrongly configured. " +
			"Please refer to the official documentation for guidance on the proper settings or contact support.")
		return &action, nil
	}

	blocked := processBody(reqState.ctx, reqState.requestBuffer, req.GetBody(), req.GetEndOfStream(), appsec.MonitorParsedHTTPBody)
	if blocked != nil && !mp.BlockingUnavailable {
		mp.instr.Logger().Debug("external_processing: request blocked, end the stream")
		actionOpts := reqState.BlockAction()
		action, err := mp.BlockMessageFunc(actionOpts)
		if err != nil {
			return nil, fmt.Errorf("error creating block message: %w", err)
		}
		return &action, io.EOF
	}

	return &action, nil
}

// OnResponseHeaders handles incoming response headers using the [ResponseHeaders] interface
// It returns a [RequestState] to be used in subsequent calls for the same request/response cycle
// along with an optional output message of type O created by either [ProcessorConfig.ContinueMessageFunc] or [ProcessorConfig.BlockMessageFunc]
// If the request is blocked or the message ends the stream, it returns io.EOF as error
func (mp *Processor[O]) OnResponseHeaders(res ResponseHeaders, reqState *RequestState) (*O, error) {
	if !reqState.State.Request() {
		return nil, fmt.Errorf("received response headers too early: %v", reqState.State)
	}

	reqState.State = MessageTypeResponseHeaders

	pseudoResponse, err := res.ExtractResponse()
	if err != nil {
		return nil, fmt.Errorf("error extracting response header from input message: %w", err)
	}

	pseudoResponse.toNetHTTP(reqState.wrappedResponseWriter)

	// We need to know if the request has been blocked, but we don't have any other way than to look for the operation and bind a blocking data listener to it
	if !mp.BlockingUnavailable {
		op, ok := dyngo.FromContext(reqState.ctx)
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
			action, err := mp.BlockMessageFunc(reqState.BlockAction())
			if err != nil {
				return nil, fmt.Errorf("error creating block message: %w", err)
			}
			return &action, io.EOF
		}

		mp.instr.Logger().Debug("message_processor: finishing request with status code: %v\n", reqState.fakeResponseWriter.status)
		return nil, io.EOF
	}

	action, err := mp.ContinueMessageFunc(ContinueActionOptions{MessageType: MessageTypeResponseHeaders, Body: reqState.State == MessageTypeResponseBody})
	if err != nil {
		return nil, fmt.Errorf("error creating continue message: %w", err)
	}

	return &action, nil
}

// OnResponseBody handles incoming response body chunks using the [HTTPBody] interface
// It uses the provided [RequestState] to keep track of the request/response cycle state
// It returns an optional output message of type O created by either [ProcessorConfig.ContinueMessageFunc] or [ProcessorConfig.BlockMessageFunc]
// If the request is blocked or the message ends the stream, it returns io.EOF as error
// Once the whole body has been received, it will try to parse it following the Content-Type header
// and if the body is not too large, it will be analyzed by the WAF
func (mp *Processor[O]) OnResponseBody(resp HTTPBody, reqState *RequestState) (*O, error) {
	if !reqState.State.Response() {
		return nil, fmt.Errorf("received response body too early: %v", reqState.State)
	}

	mp.instr.Logger().Debug("message_processor: received response body: %v - EOS: %v\n", len(resp.GetBody()), resp.GetEndOfStream())

	if mp.BodyParsingSizeLimit <= 0 || reqState.State != MessageTypeResponseBody {
		mp.instr.Logger().Error("message_processor: the body parsing has been wrongly configured. " +
			"Please refer to the official documentation for guidance on the proper settings or contact support.")
		return nil, io.EOF
	}

	blocked := processBody(reqState.ctx, reqState.responseBuffer, resp.GetBody(), resp.GetEndOfStream(), appsec.MonitorHTTPResponseBody)
	if (blocked != nil && !mp.BlockingUnavailable) || resp.GetEndOfStream() || reqState.responseBuffer.truncated {
		blockOpts := reqState.BlockAction()
		mp.instr.Logger().Debug("external_processing: request blocked, end the stream")
		blockAction, err := mp.BlockMessageFunc(blockOpts)
		if err != nil {
			return nil, fmt.Errorf("error creating block message: %w", err)
		}
		return &blockAction, io.EOF
	}

	if resp.GetEndOfStream() {
		return nil, io.EOF
	}

	continueAction, err := mp.ContinueMessageFunc(ContinueActionOptions{MessageType: MessageTypeResponseBody})
	if err != nil {
		return nil, fmt.Errorf("error creating continue message: %w", err)
	}

	return &continueAction, nil
}

// OnRequestTrailers handles incoming request trailers
func (mp *Processor[O]) OnRequestTrailers(_ *RequestState) (*O, error) {
	mp.instr.Logger().Debug("message_processor: received request trailers, ignoring")
	action, err := mp.ContinueMessageFunc(ContinueActionOptions{MessageType: MessageTypeRequestTrailers})
	if err != nil {
		return &action, fmt.Errorf("error creating continue message: %w", err)
	}

	return &action, nil
}

// OnResponseTrailers handles incoming response trailers
func (mp *Processor[O]) OnResponseTrailers(_ *RequestState) (*O, error) {
	mp.instr.Logger().Debug("message_processor: received response trailers, ignoring")
	action, err := mp.ContinueMessageFunc(ContinueActionOptions{MessageType: MessageTypeResponseTrailers})
	if err != nil {
		return &action, fmt.Errorf("error creating continue message: %w", err)
	}

	return &action, nil
}

func processBody(ctx context.Context, bodyBuffer *bodyBuffer, body []byte, eos bool, analyzeBody func(ctx context.Context, encodable any) error) error {
	bodyBuffer.append(body)

	if eos || bodyBuffer.truncated {
		return analyzeBody(ctx, json.NewEncodableFromData(bodyBuffer.buffer, bodyBuffer.truncated))
	}

	return nil
}

// isBodySupported checks if the body should be analyzed based on content type
func (mp *Processor[O]) isBodySupported(contentType string) bool {
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

func (mp *Processor[O]) Close() error {
	mp.done()
	return nil
}
