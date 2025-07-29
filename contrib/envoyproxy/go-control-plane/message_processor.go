// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gocontrolplane

import (
	"context"
	"mime"
	"strings"

	"github.com/DataDog/dd-trace-go/contrib/envoyproxy/go-control-plane/v2/internal/json"
	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/actions"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"

	envoyextprocfilter "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	envoyextproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// messageProcessor handles processing of different Envoy message types
type messageProcessor struct {
	config AppsecEnvoyConfig
}

// newMessageProcessor creates a new message processor
func newMessageProcessor(config AppsecEnvoyConfig) *messageProcessor {
	return &messageProcessor{
		config: config,
	}
}

// ProcessRequestHeaders handles incoming request headers
func (mp *messageProcessor) ProcessRequestHeaders(ctx context.Context, req *envoyextproc.ProcessingRequest_RequestHeaders) (*envoyextproc.ProcessingResponse, requestState, error) {
	instr.Logger().Debug("external_processing: received request headers: %v\n", req.RequestHeaders)

	httpReq, err := newRequest(ctx, req)
	if err != nil {
		return nil, requestState{}, status.Errorf(codes.InvalidArgument, "Error processing request headers from ext_proc: %s", err.Error())
	}

	state, blocked := newRequestState(ctx, httpReq, mp.config.BodyParsingSizeLimit, mp.config.Integration)
	if state.Span == nil {
		state.Close()
		return nil, requestState{}, status.Errorf(codes.Unknown, "Error getting span from context")
	}

	if !mp.config.BlockingUnavailable && blocked {
		state.SetBlocked()
		return buildImmediateResponse(state.FakeResponseWriter), state, nil
	}

	headerMutation, err := state.PropagationHeaders()
	if err != nil {
		state.Close()
		return nil, requestState{}, err
	}

	// Determine if we instruct Envoy to send the body with the mode override
	var modeOverride *envoyextprocfilter.ProcessingMode
	if !req.RequestHeaders.GetEndOfStream() && isBodySupported(httpReq.Header.Get("Content-Type"), mp.config) {
		modeOverride = &envoyextprocfilter.ProcessingMode{RequestBodyMode: envoyextprocfilter.ProcessingMode_STREAMED}
		state.AwaitingRequestBody = true
		// Todo: Set telemetry body size (using content-length)
	}

	processingResponse := &envoyextproc.ProcessingResponse{
		Response: &envoyextproc.ProcessingResponse_RequestHeaders{
			RequestHeaders: &envoyextproc.HeadersResponse{
				Response: &envoyextproc.CommonResponse{
					Status: envoyextproc.CommonResponse_CONTINUE,
					HeaderMutation: &envoyextproc.HeaderMutation{
						SetHeaders: headerMutation,
					},
				},
			},
		},
		// Note: Envoy should have the config "allow_mode_override" set to true to allow this override mode to be applied.
		// This is the case by default for GCP Service Extension, but not for the Envoy External Processor filter.
		ModeOverride: modeOverride,
	}

	return processingResponse, state, nil
}

// ProcessRequestBody handles incoming request body chunks
func (mp *messageProcessor) ProcessRequestBody(req *envoyextproc.ProcessingRequest_RequestBody, state *requestState) *envoyextproc.ProcessingResponse {
	instr.Logger().Debug("external_processing: received request body: %v - EOS: %v\n", len(req.RequestBody.GetBody()), req.RequestBody.EndOfStream)

	if mp.config.BodyParsingSizeLimit <= 0 || !state.AwaitingRequestBody {
		instr.Logger().Error("external_processing: the body parsing has been wrongly configured. " +
			"Please disable in your Envoy External Processor filter configuration the body processing mode and enable the allow_mode_override option to let the processor handle the processing mode.")
		return &envoyextproc.ProcessingResponse{
			Response: &envoyextproc.ProcessingResponse_RequestBody{
				RequestBody: &envoyextproc.BodyResponse{},
			},
		}
	}

	blocked := processBody(state.Ctx, state.RequestBuffer, req.RequestBody.GetBody(), req.RequestBody.GetEndOfStream(), appsec.MonitorParsedHTTPBody)
	if blocked != nil && !mp.config.BlockingUnavailable {
		state.SetBlocked()
		return buildImmediateResponse(state.FakeResponseWriter)
	}

	return &envoyextproc.ProcessingResponse{
		Response: &envoyextproc.ProcessingResponse_RequestBody{
			RequestBody: &envoyextproc.BodyResponse{},
		},
	}
}

// ProcessResponseHeaders handles incoming response headers
func (mp *messageProcessor) ProcessResponseHeaders(req *envoyextproc.ProcessingRequest_ResponseHeaders, state *requestState) (*envoyextproc.ProcessingResponse, error) {
	instr.Logger().Debug("external_processing: received response headers: %v\n", req.ResponseHeaders)
	state.AwaitingRequestBody = false

	if err := initFakeResponseWriter(state.WrappedResponseWriter, req); err != nil {
		state.Close()
		return nil, status.Errorf(codes.InvalidArgument, "Error processing response headers from ext_proc: %s", err.Error())
	}

	// We need to know if the request has been blocked, but we don't have any other way than to look for the operation and bind a blocking data listener to it
	if !mp.config.BlockingUnavailable {
		op, ok := dyngo.FromContext(state.Ctx)
		if ok {
			dyngo.OnData(op, func(_ *actions.BlockHTTP) {
				// We already wrote over the response writer, we need to reset it so the blocking handler can write to it
				httptrace.ResetStatusCode(state.WrappedResponseWriter)
				state.FakeResponseWriter.Reset()
				state.SetBlocked()
			})
		}
	}

	// Run the waf on the response headers only when we are sure to not receive a response body
	if req.ResponseHeaders.GetEndOfStream() || !isBodySupported(state.WrappedResponseWriter.Header().Get("Content-Type"), mp.config) {
		state.Close()

		if !mp.config.BlockingUnavailable && state.Blocked {
			return buildImmediateResponse(state.FakeResponseWriter), nil
		}

		instr.Logger().Debug("external_processing: finishing request with status code: %v\n", state.FakeResponseWriter.status)
		return nil, nil
	}

	// Prepare for response body
	state.AwaitingResponseBody = true

	// Todo: Set telemetry body size (using content-length)

	return &envoyextproc.ProcessingResponse{
		Response: &envoyextproc.ProcessingResponse_ResponseHeaders{
			ResponseHeaders: &envoyextproc.HeadersResponse{
				Response: &envoyextproc.CommonResponse{
					Status: envoyextproc.CommonResponse_CONTINUE,
				},
			},
		},
		ModeOverride: &envoyextprocfilter.ProcessingMode{ResponseBodyMode: envoyextprocfilter.ProcessingMode_STREAMED},
	}, nil
}

// ProcessResponseBody handles incoming response body chunks
func (mp *messageProcessor) ProcessResponseBody(req *envoyextproc.ProcessingRequest_ResponseBody, state *requestState) *envoyextproc.ProcessingResponse {
	var (
		eos  = req.ResponseBody.GetEndOfStream()
		body = req.ResponseBody.GetBody()
	)

	instr.Logger().Debug("external_processing: received response body: %v - EOS: %v\n", len(body), eos)

	blocked := processBody(state.Ctx, state.ResponseBuffer, body, eos, appsec.MonitorHTTPResponseBody)
	if blocked != nil && !mp.config.BlockingUnavailable {
		state.SetBlocked()
		return buildImmediateResponse(state.FakeResponseWriter)
	}

	if req.ResponseBody.GetEndOfStream() || state.ResponseBuffer.Truncated {
		state.Close()

		// Check for deferred blocking from response headers
		if state.Blocked && !mp.config.BlockingUnavailable {
			return buildImmediateResponse(state.FakeResponseWriter)
		}

		return nil
	}

	return &envoyextproc.ProcessingResponse{
		Response: &envoyextproc.ProcessingResponse_ResponseBody{
			ResponseBody: &envoyextproc.BodyResponse{},
		},
	}
}

func processBody(ctx context.Context, bodyBuffer *bodyBuffer, body []byte, eos bool, analyzeBody func(ctx context.Context, encodable any) error) error {
	bodyBuffer.Append(body)

	if eos || bodyBuffer.Truncated {
		return analyzeBody(ctx, json.NewEncodableFromData(bodyBuffer.Buffer, bodyBuffer.Truncated))
	}

	return nil
}

// isBodySupported checks if the body should be analyzed based on content type
func isBodySupported(contentType string, config AppsecEnvoyConfig) bool {
	if config.BodyParsingSizeLimit <= 0 {
		return false
	}

	parsedCT, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		instr.Logger().Debug("external_processing: error parsing content type '%s': %v", contentType, err)
		return false
	}

	// Handle cases like:
	// * application/json: https://www.iana.org/assignments/media-types/application/json
	// * application/vnd.api+json: https://jsonapi.org/
	// * text/json: https://mimetype.io/text/json
	return strings.HasSuffix(parsedCT, "json")
}
