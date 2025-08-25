// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package message_processor

import (
	"context"
	"fmt"
	"mime"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/actions"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"

	"github.com/DataDog/dd-trace-go/contrib/envoyproxy/go-control-plane/v2/internal/json"
)

// MessageProcessor handles processing of different message types
type MessageProcessor struct {
	config MessageProcessorConfig
	instr  *instrumentation.Instrumentation
}

// NewMessageProcessor creates a new message processor
func NewMessageProcessor(config MessageProcessorConfig, instr *instrumentation.Instrumentation) MessageProcessor {
	return MessageProcessor{
		config: config,
		instr:  instr,
	}
}

// OnRequestHeaders handles incoming request headers
func (mp *MessageProcessor) OnRequestHeaders(ctx context.Context, req RequestHeaders) (*RequestState, Action, error) {
	httpReq, err := req.NewRequest(ctx)
	if err != nil {
		return nil, Action{}, err
	}

	componentName := req.Component(ctx)
	framework := req.Framework()
	reqState, blocked := newRequestState(httpReq, mp.instr, mp.config.BodyParsingSizeLimit, componentName, framework)
	if reqState == nil {
		return nil, Action{}, fmt.Errorf("error getting span from context")
	}

	if !mp.config.BlockingUnavailable && blocked {
		reqState.SetBlocked()
		return reqState, newBlockAction(reqState.fakeResponseWriter), nil
	}

	headerMutation, err := reqState.PropagationHeaders()
	if err != nil {
		reqState.Close()
		return nil, Action{}, err
	}

	// Determine if we instruct the proxy to send the body
	var requestBody bool
	if !req.EndOfStream() && mp.isBodySupported(httpReq.Header.Get("Content-Type"), mp.config) {
		requestBody = true
		reqState.AwaitingRequestBody = true
		// Todo: Set telemetry body size (using content-length)
	}

	return reqState, newContinueActionWithResponseData(headerMutation, requestBody, DirectionRequest), nil
}

// OnRequestBody handles incoming request body chunks
func (mp *MessageProcessor) OnRequestBody(req RequestBody, reqState *RequestState) (Action, error) {
	if mp.config.BodyParsingSizeLimit <= 0 || !reqState.AwaitingRequestBody {
		mp.instr.Logger().Error("message_processor: the body parsing has been wrongly configured. " +
			"Please refer to the official documentation for guidance on the proper settings or contact support.")
		return newContinueAction(), nil
	}

	blocked := processBody(reqState.ctx, reqState.requestBuffer, req.Body(), req.EndOfStream(), appsec.MonitorParsedHTTPBody)
	if blocked != nil && !mp.config.BlockingUnavailable {
		reqState.SetBlocked()
		return newBlockAction(reqState.fakeResponseWriter), nil
	}

	return newContinueAction(), nil
}

// OnResponseHeaders handles incoming response headers
func (mp *MessageProcessor) OnResponseHeaders(res ResponseHeaders, reqState *RequestState) (Action, error) {
	mp.instr.Logger().Debug("message_processor: received response headers")
	reqState.AwaitingRequestBody = false

	if err := res.InitResponseWriter(reqState.wrappedResponseWriter); err != nil {
		reqState.Close()
		return Action{}, fmt.Errorf("error processing response headers: %s", err)
	}

	// We need to know if the request has been blocked, but we don't have any other way than to look for the operation and bind a blocking data listener to it
	if !mp.config.BlockingUnavailable {
		op, ok := dyngo.FromContext(reqState.ctx)
		if ok {
			dyngo.OnData(op, func(_ *actions.BlockHTTP) {
				// We already wrote over the response writer, we need to reset it so the blocking MessageProcessor can write to it
				httptrace.ResetStatusCode(reqState.wrappedResponseWriter)
				reqState.fakeResponseWriter.Reset()
				reqState.SetBlocked()
			})
		}
	}

	// Run the waf on the response headers only when we are sure to not receive a response body
	if res.EndOfStream() || !mp.isBodySupported(reqState.wrappedResponseWriter.Header().Get("Content-Type"), mp.config) {
		reqState.Close()

		if !mp.config.BlockingUnavailable && reqState.blocked {
			return newBlockAction(reqState.fakeResponseWriter), nil
		}

		mp.instr.Logger().Debug("message_processor: finishing request with status code: %v\n", reqState.fakeResponseWriter.status)
		return newFinishAction(), nil
	}

	// Prepare for response body
	reqState.AwaitingResponseBody = true

	// Todo: Set telemetry body size (using content-length)

	return newContinueActionWithResponseData(nil, true, DirectionResponse), nil
}

// OnResponseBody handles incoming response body chunks
func (mp *MessageProcessor) OnResponseBody(res ResponseBody, reqState *RequestState) (Action, error) {
	mp.instr.Logger().Debug("message_processor: received response body: %v - EOS: %v\n", len(res.Body()), res.EndOfStream())

	if mp.config.BodyParsingSizeLimit <= 0 || !reqState.AwaitingResponseBody {
		mp.instr.Logger().Error("message_processor: the body parsing has been wrongly configured. " +
			"Please refer to the official documentation for guidance on the proper settings or contact support.")
		return newContinueAction(), nil
	}

	blocked := processBody(reqState.ctx, reqState.responseBuffer, res.Body(), res.EndOfStream(), appsec.MonitorHTTPResponseBody)
	if blocked != nil && !mp.config.BlockingUnavailable {
		reqState.SetBlocked()
		return newBlockAction(reqState.fakeResponseWriter), nil
	}

	if res.EndOfStream() || reqState.responseBuffer.truncated {
		reqState.Close()

		// Check for deferred blocking from response headers
		if reqState.blocked && !mp.config.BlockingUnavailable {
			return newBlockAction(reqState.fakeResponseWriter), nil
		}

		return newFinishAction(), nil
	}

	return newContinueAction(), nil
}

// OnRequestTrailers handles incoming request trailers
func (mp *MessageProcessor) OnRequestTrailers(_ RequestState) (Action, error) {
	mp.instr.Logger().Debug("message_processor: received request trailers, ignoring")
	return newContinueAction(), nil
}

// OnResponseTrailers handles incoming response trailers
func (mp *MessageProcessor) OnResponseTrailers(_ RequestState) (Action, error) {
	mp.instr.Logger().Debug("message_processor: received response trailers, ignoring")
	return newContinueAction(), nil
}

func processBody(ctx context.Context, bodyBuffer *bodyBuffer, body []byte, eos bool, analyzeBody func(ctx context.Context, encodable any) error) error {
	bodyBuffer.append(body)

	if eos || bodyBuffer.truncated {
		return analyzeBody(ctx, json.NewEncodableFromData(bodyBuffer.buffer, bodyBuffer.truncated))
	}

	return nil
}

// isBodySupported checks if the body should be analyzed based on content type
func (mp *MessageProcessor) isBodySupported(contentType string, config MessageProcessorConfig) bool {
	if config.BodyParsingSizeLimit <= 0 {
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
