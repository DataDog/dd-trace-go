// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package go_control_plane

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/waf/actions"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	envoycore "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoyextproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	envoytypes "github.com/envoyproxy/go-control-plane/envoy/type/v3"
)

const componentName = "envoyproxy/go-control-plane"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/envoyproxy/go-control-plane")
}

// appsecEnvoyExternalProcessorServer is a server that implements the Envoy ExternalProcessorServer interface.
type appsecEnvoyExternalProcessorServer struct {
	envoyextproc.ExternalProcessorServer
}

// AppsecEnvoyExternalProcessorServer creates and returns a new instance of appsecEnvoyExternalProcessorServer.
func AppsecEnvoyExternalProcessorServer(userImplementation envoyextproc.ExternalProcessorServer) envoyextproc.ExternalProcessorServer {
	return &appsecEnvoyExternalProcessorServer{userImplementation}
}

type currentRequest struct {
	span                  tracer.Span
	afterHandle           func()
	ctx                   context.Context
	fakeResponseWriter    *fakeResponseWriter
	wrappedResponseWriter http.ResponseWriter
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
		blocked            bool
		currentRequest     *currentRequest
		processingRequest  envoyextproc.ProcessingRequest
		processingResponse *envoyextproc.ProcessingResponse
	)

	// Close the span when the request is done processing
	defer func() {
		if currentRequest != nil {
			log.Warn("external_processing: stream stopped during a request, making sure the current span is closed\n")
			currentRequest.span.Finish()
			currentRequest = nil
		}
	}()

	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil
			}

			return ctx.Err()

		default:
		}

		err := processServer.RecvMsg(&processingRequest)
		if err != nil {
			// Note: Envoy is inconsistent with the "end_of_stream" value of its headers responses,
			// so we can't fully rely on it to determine when it will close (cancel) the stream.
			if s, ok := status.FromError(err); (ok && s.Code() == codes.Canceled) || err == io.EOF {
				return nil
			}

			log.Warn("external_processing: error receiving request/response: %v\n", err)
			return status.Errorf(codes.Unknown, "Error receiving request/response: %v", err)
		}

		processingResponse, err = envoyExternalProcessingRequestTypeAssert(&processingRequest)
		if err != nil {
			log.Error("external_processing: error asserting request type: %v\n", err)
			return status.Errorf(codes.Unknown, "Error asserting request type: %v", err)
		}

		switch v := processingRequest.Request.(type) {
		case *envoyextproc.ProcessingRequest_RequestHeaders:
			processingResponse, currentRequest, blocked, err = processRequestHeaders(ctx, v)
		case *envoyextproc.ProcessingRequest_ResponseHeaders:
			processingResponse, err = processResponseHeaders(v, currentRequest)
			currentRequest = nil // Request is done, reset the current request
		}

		if err != nil {
			log.Error("external_processing: error processing request: %v\n", err)
			return err
		}

		// End of stream reached, no more data to process
		if processingResponse == nil {
			log.Debug("external_processing: end of stream reached")
			return nil
		}

		if err := processServer.SendMsg(processingResponse); err != nil {
			log.Warn("external_processing: error sending response (probably because of an Envoy timeout): %v", err)
			return status.Errorf(codes.Unknown, "Error sending response (probably because of an Envoy timeout): %v", err)
		}

		if blocked {
			log.Debug("external_processing: request blocked, end the stream")
			currentRequest = nil
			return nil
		}
	}
}

func envoyExternalProcessingRequestTypeAssert(req *envoyextproc.ProcessingRequest) (*envoyextproc.ProcessingResponse, error) {
	switch r := req.Request.(type) {
	case *envoyextproc.ProcessingRequest_RequestHeaders, *envoyextproc.ProcessingRequest_ResponseHeaders:
		return nil, nil

	case *envoyextproc.ProcessingRequest_RequestBody:
		// TODO: Handle request raw body in the WAF
		return &envoyextproc.ProcessingResponse{
			Response: &envoyextproc.ProcessingResponse_RequestBody{
				RequestBody: &envoyextproc.BodyResponse{
					Response: &envoyextproc.CommonResponse{
						Status: envoyextproc.CommonResponse_CONTINUE,
					},
				},
			},
		}, nil

	case *envoyextproc.ProcessingRequest_RequestTrailers:
		return &envoyextproc.ProcessingResponse{
			Response: &envoyextproc.ProcessingResponse_RequestTrailers{},
		}, nil

	case *envoyextproc.ProcessingRequest_ResponseBody:
		// Note: The end of stream bool value is not reliable
		// Sometimes it's not set to true even if there is no more data to process
		if r.ResponseBody.GetEndOfStream() {
			return nil, nil
		}

		// TODO: Handle response raw body in the WAF
		return &envoyextproc.ProcessingResponse{
			Response: &envoyextproc.ProcessingResponse_ResponseBody{},
		}, nil

	case *envoyextproc.ProcessingRequest_ResponseTrailers:
		return &envoyextproc.ProcessingResponse{
			Response: &envoyextproc.ProcessingResponse_RequestTrailers{},
		}, nil

	default:
		return nil, status.Errorf(codes.Unknown, "Unknown request type: %T", r)
	}
}

func processRequestHeaders(ctx context.Context, req *envoyextproc.ProcessingRequest_RequestHeaders) (*envoyextproc.ProcessingResponse, *currentRequest, bool, error) {
	log.Debug("external_processing: received request headers: %v\n", req.RequestHeaders)

	request, err := newRequest(ctx, req)
	if err != nil {
		return nil, nil, false, status.Errorf(codes.InvalidArgument, "Error processing request headers from ext_proc: %v", err)
	}

	var blocked bool
	fakeResponseWriter := newFakeResponseWriter()
	wrappedResponseWriter, request, afterHandle, blocked := httptrace.BeforeHandle(&httptrace.ServeConfig{
		SpanOpts: []ddtrace.StartSpanOption{
			tracer.Tag(ext.SpanKind, ext.SpanKindServer),
			tracer.Tag(ext.Component, componentName),
		},
	}, fakeResponseWriter, request)

	// Block handling: If triggered, we need to block the request, return an immediate response
	if blocked {
		afterHandle()
		return doBlockResponse(fakeResponseWriter), nil, true, nil
	}

	span, ok := tracer.SpanFromContext(request.Context())
	if !ok {
		return nil, nil, false, status.Errorf(codes.Unknown, "Error getting span from context")
	}

	processingResponse, err := propagationRequestHeaderMutation(span)
	if err != nil {
		return nil, nil, false, err
	}

	return processingResponse, &currentRequest{
		span:                  span,
		ctx:                   request.Context(),
		fakeResponseWriter:    fakeResponseWriter,
		wrappedResponseWriter: wrappedResponseWriter,
		afterHandle:           afterHandle,
	}, false, nil
}

func propagationRequestHeaderMutation(span ddtrace.Span) (*envoyextproc.ProcessingResponse, error) {
	newHeaders := make(http.Header)
	if err := tracer.Inject(span.Context(), tracer.HTTPHeadersCarrier(newHeaders)); err != nil {
		return nil, status.Errorf(codes.Unknown, "Error injecting headers: %v", err)
	}

	if len(newHeaders) > 0 {
		log.Debug("external_processing: injecting propagation headers: %v\n", newHeaders)
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

	return &envoyextproc.ProcessingResponse{
		Response: &envoyextproc.ProcessingResponse_RequestHeaders{
			RequestHeaders: &envoyextproc.HeadersResponse{
				Response: &envoyextproc.CommonResponse{
					Status: envoyextproc.CommonResponse_CONTINUE,
					HeaderMutation: &envoyextproc.HeaderMutation{
						SetHeaders: headerValueOptions,
					},
				},
			},
		},
	}, nil
}

func processResponseHeaders(res *envoyextproc.ProcessingRequest_ResponseHeaders, currentRequest *currentRequest) (*envoyextproc.ProcessingResponse, error) {
	log.Debug("external_processing: received response headers: %v\n", res.ResponseHeaders)

	if err := createFakeResponseWriter(currentRequest.wrappedResponseWriter, res); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "Error processing response headers from ext_proc: %v", err)
	}

	var blocked bool

	// Now we need to know if the request has been blocked, but we don't have any other way than to look for the operation and bind a blocking data listener to it
	op, ok := dyngo.FromContext(currentRequest.ctx)
	if ok {
		dyngo.OnData(op, func(_ *actions.BlockHTTP) {
			// We already wrote over the response writer, we need to reset it so the blocking handler can write to it
			httptrace.ResetStatusCode(currentRequest.wrappedResponseWriter)
			currentRequest.fakeResponseWriter.Reset()
			blocked = true
		})
	}

	currentRequest.afterHandle()

	if blocked {
		response := doBlockResponse(currentRequest.fakeResponseWriter)
		return response, nil
	}

	log.Debug("external_processing: finishing request with status code: %v\n", currentRequest.fakeResponseWriter.status)

	// Note: (cf. comment in the stream error handling)
	// The end of stream bool value is not reliable
	if res.ResponseHeaders.GetEndOfStream() {
		return nil, nil
	}

	return &envoyextproc.ProcessingResponse{
		Response: &envoyextproc.ProcessingResponse_ResponseHeaders{
			ResponseHeaders: &envoyextproc.HeadersResponse{
				Response: &envoyextproc.CommonResponse{
					Status: envoyextproc.CommonResponse_CONTINUE,
				},
			},
		},
	}, nil
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

	var int32StatusCode int32 = 0
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
				Body: string(writer.body),
				GrpcStatus: &envoyextproc.GrpcStatus{
					Status: 0,
				},
			},
		},
	}
}
