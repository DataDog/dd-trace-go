// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gocontrolplane

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/contrib/envoyproxy/go-control-plane/v2/internal/json"
	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/actions"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	envoycore "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoyextprocfilter "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	envoyextproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	envoytypes "github.com/envoyproxy/go-control-plane/envoy/type/v3"
)

const componentNameEnvoy = "envoyproxy/go-control-plane"
const componentNameGCPServiceExtension = "gcp-service-extension"

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageEnvoyProxyGoControlPlane)
}

type AppsecEnvoyConfig struct {
	IsGCPServiceExtension bool
	BlockingUnavailable   bool
	Context               context.Context
	BodyParsingSizeLimit  int
}

// appsecEnvoyExternalProcessorServer is a server that implements the Envoy ExternalProcessorServer interface.
type appsecEnvoyExternalProcessorServer struct {
	envoyextproc.ExternalProcessorServer
	config         AppsecEnvoyConfig
	requestCounter atomic.Uint32
}

func AppsecEnvoyExternalProcessorServer(userImplementation envoyextproc.ExternalProcessorServer, config AppsecEnvoyConfig) envoyextproc.ExternalProcessorServer {
	processor := &appsecEnvoyExternalProcessorServer{
		ExternalProcessorServer: userImplementation,
		config:                  config,
	}

	if config.Context != nil {
		go func() {
			ticker := time.NewTicker(1 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					instr.Logger().Info("external_processing: analyzed %d requests in the last minute", processor.requestCounter.Swap(0))
				case <-config.Context.Done():
					return
				}
			}
		}()
	}

	if config.BodyParsingSizeLimit <= 0 {
		instr.Logger().Info("external_processing: body parsing size limit set to 0 or negative. The body of requests and responses will not be analyzed.")
	}

	return processor
}

type requestContext struct {
	span                   *tracer.Span
	afterHandle            func()
	ctx                    context.Context
	fakeResponseWriter     *fakeResponseWriter
	wrappedResponseWriter  http.ResponseWriter
	bodyBuffer             []byte
	bodyTruncated          bool
	receivingRequestBody   bool
	waitingForResponseBody bool
	blockedResponseHeaders bool
}

// Process handles the bidirectional stream that Envoy uses to give the server control
// over what the filter does. It processes incoming requests and sends appropriate responses
// based on the type of request received.
//
// The method receive incoming requests, processes them, and sends responses back to the client.
// It handles different types of requests such as request headers, response headers, request body,
// response body, request trailers, and response trailers.
//
// If the request is blocked, it sends an immediate response and ends the stream. If an error occurs
// during processing, it logs the error and returns an appropriate gRPC status error.
func (s *appsecEnvoyExternalProcessorServer) Process(processServer envoyextproc.ExternalProcessor_ProcessServer) error {
	var (
		ctx                = processServer.Context()
		currentRequest     *requestContext
		processingRequest  envoyextproc.ProcessingRequest
		processingResponse *envoyextproc.ProcessingResponse
	)

	// Close the span when the request is done processing
	defer func() {
		s.requestCounter.Add(1)

		if currentRequest == nil {
			return
		}

		// We can't know if Envoy is configured to send the request and response body or not, but we were waiting for it,
		// so don't show an error as it is expected in that specific configuration.
		if !currentRequest.waitingForResponseBody {
			instr.Logger().Warn("external_processing: stream stopped during a request, making sure the current span is closed\n")
		}

		currentRequest.afterHandle()
		currentRequest = nil
	}()

	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil
			}

			return ctx.Err()
		default:
			// no op
		}

		err := processServer.RecvMsg(&processingRequest)
		if err != nil {
			if s, ok := status.FromError(err); (ok && s.Code() == codes.Canceled) || err == io.EOF {
				return nil
			}

			instr.Logger().Warn("external_processing: error receiving request/response: %v\n", err)
			return status.Errorf(codes.Unknown, "Error receiving request/response: %v", err)
		}

		processingResponse, err = envoyExternalProcessingRequestTypeAssert(&processingRequest)
		if err != nil {
			instr.Logger().Error("external_processing: error asserting request type: %v\n", err)
			return status.Errorf(codes.Unknown, "Error asserting request type: %v", err)
		}

		var blocked bool

		switch v := processingRequest.Request.(type) {
		case *envoyextproc.ProcessingRequest_RequestHeaders:
			processingResponse, currentRequest, blocked, err = processRequestHeaders(ctx, v, s.config)
		case *envoyextproc.ProcessingRequest_RequestBody:
			processingResponse = processRequestBody(v, currentRequest, s.config)

		case *envoyextproc.ProcessingRequest_ResponseHeaders:
			processingResponse, blocked, err = processResponseHeaders(v, currentRequest, s.config)
		case *envoyextproc.ProcessingRequest_ResponseBody:
			processingResponse = processResponseBody(v, currentRequest, s.config)

		default:
			// no op
		}

		if err != nil {
			instr.Logger().Error("external_processing: error processing request: %v\n", err)
			return err
		}

		// End of stream reached, no more data to process
		// We are sure that nothing more needs to be analyzed, no need to send a response (can only happen when response headers are received (with no body) or after a request body is received)
		// and that the request is not blocked
		if processingResponse == nil {
			instr.Logger().Debug("external_processing: end of stream reached")
			currentRequest = nil // Must set it to nil to indicate that the span was closed without issue
			return nil
		}

		instr.Logger().Debug("external_processing: sending response: %v\n", processingResponse)
		if err := processServer.SendMsg(processingResponse); err != nil {
			instr.Logger().Warn("external_processing: error sending response (probably because of an Envoy timeout): %v", err)
			return status.Errorf(codes.Unknown, "Error sending response (probably because of an Envoy timeout): %v", err)
		}

		if blocked {
			instr.Logger().Debug("external_processing: request blocked, end the stream")
			return nil
		}
	}
}

func envoyExternalProcessingRequestTypeAssert(req *envoyextproc.ProcessingRequest) (*envoyextproc.ProcessingResponse, error) {
	switch r := req.Request.(type) {
	case *envoyextproc.ProcessingRequest_RequestHeaders, *envoyextproc.ProcessingRequest_ResponseHeaders:
		return nil, nil

	case *envoyextproc.ProcessingRequest_RequestBody:
		return nil, nil

	case *envoyextproc.ProcessingRequest_RequestTrailers:
		return &envoyextproc.ProcessingResponse{
			Response: &envoyextproc.ProcessingResponse_RequestTrailers{},
		}, nil

	case *envoyextproc.ProcessingRequest_ResponseBody:
		return nil, nil

	case *envoyextproc.ProcessingRequest_ResponseTrailers:
		return &envoyextproc.ProcessingResponse{
			Response: &envoyextproc.ProcessingResponse_RequestTrailers{},
		}, nil

	default:
		return nil, status.Errorf(codes.Unknown, "Unknown request type: %T", r)
	}
}

func processRequestHeaders(ctx context.Context, req *envoyextproc.ProcessingRequest_RequestHeaders, config AppsecEnvoyConfig) (*envoyextproc.ProcessingResponse, *requestContext, bool, error) {
	instr.Logger().Debug("external_processing: received request headers: %v\n", req.RequestHeaders)

	request, err := newRequest(ctx, req)
	if err != nil {
		return nil, nil, false, status.Errorf(codes.InvalidArgument, "Error processing request headers from ext_proc: %v", err)
	}

	var spanComponentName string
	if config.IsGCPServiceExtension {
		spanComponentName = componentNameGCPServiceExtension
	} else {
		spanComponentName = componentNameEnvoy
	}

	var blocked bool
	fakeResponseWriter := newFakeResponseWriter()
	wrappedResponseWriter, request, afterHandle, blocked := httptrace.BeforeHandle(&httptrace.ServeConfig{
		Framework: "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3",
		Resource:  request.Method + " " + path.Clean(request.URL.Path),
		SpanOpts: []tracer.StartSpanOption{
			tracer.Tag(ext.SpanKind, ext.SpanKindServer),
			tracer.Tag(ext.Component, spanComponentName),
		},
	}, fakeResponseWriter, request)

	// Block handling: If triggered, we need to block the request, return an immediate response
	if !config.BlockingUnavailable && blocked {
		afterHandle()
		return doBlockResponse(fakeResponseWriter), nil, true, nil
	}

	span, ok := tracer.SpanFromContext(request.Context())
	if !ok {
		afterHandle()
		return nil, nil, false, status.Errorf(codes.Unknown, "Error getting span from context")
	}

	headerMutation, err := propagationRequestHeaderMutation(span)
	if err != nil {
		afterHandle()
		return nil, nil, false, err
	}

	// Note: Envoy should have the config "allow_mode_override" set to true to allow this override mode to be applied.
	// This is the case by default for GCP Service Extension, but not for the Envoy External Processor filter.
	var modeOverride *envoyextprocfilter.ProcessingMode
	if !req.RequestHeaders.GetEndOfStream() && isBodySupported(request.Header.Get("Content-Type"), config) {
		modeOverride = &envoyextprocfilter.ProcessingMode{RequestBodyMode: envoyextprocfilter.ProcessingMode_STREAMED}
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
		ModeOverride: modeOverride,
	}

	return processingResponse, &requestContext{
		span:                  span,
		ctx:                   request.Context(),
		fakeResponseWriter:    fakeResponseWriter,
		wrappedResponseWriter: wrappedResponseWriter,
		afterHandle:           afterHandle,
	}, false, nil
}

func propagationRequestHeaderMutation(span *tracer.Span) ([]*envoycore.HeaderValueOption, error) {
	newHeaders := make(http.Header)
	if err := tracer.Inject(span.Context(), tracer.HTTPHeadersCarrier(newHeaders)); err != nil {
		return nil, status.Errorf(codes.Unknown, "Error injecting headers: %v", err)
	}

	if len(newHeaders) > 0 {
		instr.Logger().Debug("external_processing: injecting propagation headers: %v\n", newHeaders)
	}

	headerValueOptions := make([]*envoycore.HeaderValueOption, 0, len(newHeaders))
	for k, v := range newHeaders {
		headerValueOptions = append(headerValueOptions, &envoycore.HeaderValueOption{
			Header: &envoycore.HeaderValue{
				Key:      k,
				RawValue: []byte(strings.Join(v, ",")),
			},
		})
	}

	return headerValueOptions, nil
}

// isBodySupported checks if the body should be analyzed.
func isBodySupported(contentTypeValue string, config AppsecEnvoyConfig) bool {
	if config.BodyParsingSizeLimit <= 0 {
		return true
	}

	values := strings.Split(contentTypeValue, ";")
	for _, v := range values {
		if strings.HasSuffix(strings.TrimSpace(v), "json") {
			return true
		}
	}

	return false
}

func processResponseHeaders(res *envoyextproc.ProcessingRequest_ResponseHeaders, currentRequest *requestContext, config AppsecEnvoyConfig) (*envoyextproc.ProcessingResponse, bool, error) {
	instr.Logger().Debug("external_processing: received response headers: %v\n", res.ResponseHeaders)

	if currentRequest == nil {
		// Can happen when a malformed request is sent to Envoy (with no header), the request is never sent to the External Processor and directly passed to the server
		// However the response of the server (which is valid) is sent to the External Processor and fail to be processed
		instr.Logger().Warn("external_processing: can't process the response: envoy never sent the beginning of the request, this is a known issue" +
			" and can happen when a malformed request is sent to Envoy where the header Host is missing. See link to issue https://github.com/envoyproxy/envoy/issues/38022")
		return nil, false, status.Errorf(codes.InvalidArgument, "Error processing response headers from ext_proc: can't process the response")
	}

	if err := createFakeResponseWriter(currentRequest.wrappedResponseWriter, res); err != nil {
		currentRequest.afterHandle()
		return nil, false, status.Errorf(codes.InvalidArgument, "Error processing response headers from ext_proc: %v", err)
	}

	// Now we need to know if the request has been blocked, but we don't have any other way than to look for the operation and bind a blocking data listener to it
	if !config.BlockingUnavailable {
		op, ok := dyngo.FromContext(currentRequest.ctx)
		if ok {
			dyngo.OnData(op, func(_ *actions.BlockHTTP) {
				// We already wrote over the response writer, we need to reset it so the blocking handler can write to it
				httptrace.ResetStatusCode(currentRequest.wrappedResponseWriter)
				currentRequest.fakeResponseWriter.Reset()
				currentRequest.blockedResponseHeaders = true
			})
		}
	}

	// Run the waf on the response headers only when we are sure to not receive a response body
	if res.ResponseHeaders.GetEndOfStream() || !isBodySupported(currentRequest.wrappedResponseWriter.Header().Get("Content-Type"), config) {
		currentRequest.afterHandle()

		if !config.BlockingUnavailable && currentRequest.blockedResponseHeaders {
			return doBlockResponse(currentRequest.fakeResponseWriter), true, nil
		}

		// We can end the stream as no response body is expected or the body is not supported
		instr.Logger().Debug("external_processing: finishing request with status code: %v\n", currentRequest.fakeResponseWriter.status)
		return nil, false, nil
	}

	currentRequest.waitingForResponseBody = true // To not output warns if the connection is closed before the response body is received

	return &envoyextproc.ProcessingResponse{
		Response: &envoyextproc.ProcessingResponse_ResponseHeaders{
			ResponseHeaders: &envoyextproc.HeadersResponse{
				Response: &envoyextproc.CommonResponse{
					Status: envoyextproc.CommonResponse_CONTINUE,
				},
			},
		},
		// Note: Envoy should have the config "allow_mode_override" set to true to allow this override mode to be applied.
		// This is the case by default for GCP Service Extension, but not for the Envoy External Processor filter.
		ModeOverride: &envoyextprocfilter.ProcessingMode{ResponseBodyMode: envoyextprocfilter.ProcessingMode_STREAMED},
	}, false, nil
}

func processRequestBody(body *envoyextproc.ProcessingRequest_RequestBody, currentRequest *requestContext, config AppsecEnvoyConfig) *envoyextproc.ProcessingResponse {
	instr.Logger().Debug("external_processing: received request body: %v - EOS: %v\n", len(body.RequestBody.GetBody()), body.RequestBody.EndOfStream)

	eos := body.RequestBody.GetEndOfStream()

	if config.BodyParsingSizeLimit <= 0 {
		instr.Logger().Warn("external_processing: the body parsing has been wrongly configured. " +
			"Please disable in your Envoy External Processor filter configuration the body processing mode.")

		return &envoyextproc.ProcessingResponse{
			Response: &envoyextproc.ProcessingResponse_RequestBody{
				RequestBody: &envoyextproc.BodyResponse{},
			},
		}
	}

	currentRequest.receivingRequestBody = true
	blocked := processBody(body.RequestBody.GetBody(), eos, currentRequest, config.BodyParsingSizeLimit, true)
	if blocked != nil && !config.BlockingUnavailable {
		currentRequest.afterHandle()
		return doBlockResponse(currentRequest.fakeResponseWriter)
	}

	return &envoyextproc.ProcessingResponse{
		Response: &envoyextproc.ProcessingResponse_RequestBody{
			RequestBody: &envoyextproc.BodyResponse{},
		},
	}
}

func processResponseBody(body *envoyextproc.ProcessingRequest_ResponseBody, currentRequest *requestContext, config AppsecEnvoyConfig) *envoyextproc.ProcessingResponse {
	instr.Logger().Debug("external_processing: received response body: %v - EOS: %v\n", len(body.ResponseBody.GetBody()), body.ResponseBody.EndOfStream)

	// If the response headers came before the end of the request body, we can be in a state where the request body
	// was not fully received and was not processed.
	if currentRequest.receivingRequestBody {
		currentRequest.receivingRequestBody = false
		currentRequest.bodyBuffer = nil
		currentRequest.bodyTruncated = false
	}

	eos := body.ResponseBody.GetEndOfStream()

	blocked := processBody(body.ResponseBody.GetBody(), eos, currentRequest, config.BodyParsingSizeLimit, false)
	if blocked != nil && !config.BlockingUnavailable {
		currentRequest.afterHandle()
		return doBlockResponse(currentRequest.fakeResponseWriter)
	}

	// When the request is not blocked and no more data is expected, we can end the stream
	if eos {
		currentRequest.afterHandle()

		// Because the afterHandle for the response headers is not executed when a response body is awaited,
		// the blocking event of response headers can be triggered now.
		if currentRequest.blockedResponseHeaders && !config.BlockingUnavailable {
			return doBlockResponse(currentRequest.fakeResponseWriter)
		}

		return nil
	}

	return &envoyextproc.ProcessingResponse{
		Response: &envoyextproc.ProcessingResponse_ResponseBody{
			ResponseBody: &envoyextproc.BodyResponse{},
		},
	}
}

// processBody is called when a processing request/response body is received from Envoy.
// The body can be received in multiple chunks, so we need to buffer the data until the end of the request.
// Returns an error when the request needs to be blocked, nil otherwise.
func processBody(bodyChunk []byte, eos bool, currentRequest *requestContext, bodyParsingSizeLimit int, isRequest bool) error {
	bodyLength := len(bodyChunk)

	// Only add the bytes to the buffer that can fit
	if bodyLength > 0 && !currentRequest.bodyTruncated {
		currentBufferBodyLength := len(currentRequest.bodyBuffer)

		if currentBufferBodyLength+bodyLength > bodyParsingSizeLimit {
			bodyLength = bodyParsingSizeLimit - currentBufferBodyLength
			instr.Logger().Debug("external_processing: request body size limit reached, truncating body to %d bytes", bodyLength)
			currentRequest.bodyTruncated = true
		}

		if currentRequest.bodyBuffer == nil {
			currentRequest.bodyBuffer = make([]byte, 0, bodyLength)
		}

		currentRequest.bodyBuffer = append(currentRequest.bodyBuffer, bodyChunk[:bodyLength]...)
	}

	// Only run the analysis on the body when it's complete or if it has been truncated
	if !eos && !currentRequest.bodyTruncated {
		instr.Logger().Debug("external_processing: request body not complete, waiting for more data")
		return nil
	}

	instr.Logger().Debug("external_processing: request body complete or max size, processing body")

	defer func() {
		currentRequest.bodyTruncated = false
		currentRequest.bodyBuffer = nil
		currentRequest.receivingRequestBody = false
	}()

	jsonEncoder := json.NewEncodable(currentRequest.bodyBuffer, currentRequest.bodyTruncated)

	if isRequest {
		return appsec.MonitorParsedHTTPBody(currentRequest.ctx, jsonEncoder)
	}
	return appsec.MonitorHTTPResponseBody(currentRequest.ctx, jsonEncoder)
}

func doBlockResponse(writer *fakeResponseWriter) *envoyextproc.ProcessingResponse {
	var headersMutation []*envoycore.HeaderValueOption
	for k, v := range writer.headers {
		headersMutation = append(headersMutation, &envoycore.HeaderValueOption{
			Header: &envoycore.HeaderValue{
				Key:      k,
				RawValue: []byte(strings.Join(v, ",")),
			},
		})
	}

	var int32StatusCode int32
	if writer.status > 0 && writer.status <= math.MaxInt32 {
		int32StatusCode = int32(writer.status)
	}

	return &envoyextproc.ProcessingResponse{
		Response: &envoyextproc.ProcessingResponse_ImmediateResponse{
			ImmediateResponse: &envoyextproc.ImmediateResponse{
				Status: &envoytypes.HttpStatus{
					Code: envoytypes.StatusCode(int32StatusCode),
				},
				Headers: &envoyextproc.HeaderMutation{
					SetHeaders: headersMutation,
				},
				Body: writer.body,
				GrpcStatus: &envoyextproc.GrpcStatus{
					Status: 0,
				},
			},
		},
	}
}
