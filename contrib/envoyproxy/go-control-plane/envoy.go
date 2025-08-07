// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gocontrolplane

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/contrib/envoyproxy/go-control-plane/v2/message_processor"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	envoyextproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageEnvoyProxyGoControlPlane)
}

// Integration represents the proxy integration type that is used for the External Processing.
type Integration int

const (
	GCPServiceExtensionIntegration Integration = iota
	EnvoyIntegration
	IstioIntegration
)

// AppsecEnvoyConfig contains configuration for the AppSec Envoy processor
type AppsecEnvoyConfig struct {
	Integration          Integration
	BlockingUnavailable  bool
	Context              context.Context
	BodyParsingSizeLimit int
}

// appsecEnvoyExternalProcessorServer is a server that implements the Envoy ExternalProcessorServer interface.
type appsecEnvoyExternalProcessorServer struct {
	envoyextproc.ExternalProcessorServer
	config           AppsecEnvoyConfig
	requestCounter   atomic.Uint32
	messageProcessor message_processor.MessageProcessor
}

// AppsecEnvoyExternalProcessorServer creates a new external processor server with AAP enabled
func AppsecEnvoyExternalProcessorServer(userImplementation envoyextproc.ExternalProcessorServer, config AppsecEnvoyConfig) envoyextproc.ExternalProcessorServer {
	processor := &appsecEnvoyExternalProcessorServer{
		ExternalProcessorServer: userImplementation,
		config:                  config,
		messageProcessor: message_processor.NewMessageProcessor(message_processor.MessageProcessorConfig{
			BlockingUnavailable:  config.BlockingUnavailable,
			BodyParsingSizeLimit: config.BodyParsingSizeLimit,
		}, instr),
	}

	if config.Context != nil {
		processor.startMetricsReporter(config.Context)
	}

	if config.BodyParsingSizeLimit <= 0 {
		instr.Logger().Info("external_processing: body parsing size limit set to 0 or negative. The request and response bodies will be ignored.")
	}

	switch config.Integration {
	case GCPServiceExtensionIntegration, EnvoyIntegration, IstioIntegration:
	default:
		instr.Logger().Error("external_processing: invalid proxy integration type %d. Defaulting to GCPServiceExtensionIntegration", config.Integration)
		config.Integration = GCPServiceExtensionIntegration
	}

	return processor
}

// startMetricsReporter starts a background goroutine to report request metrics
func (s *appsecEnvoyExternalProcessorServer) startMetricsReporter(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				instr.Logger().Info("external_processing: analyzed %d requests in the last minute", s.requestCounter.Swap(0))
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Process handles the bidirectional stream that Envoy uses to control the filter
func (s *appsecEnvoyExternalProcessorServer) Process(processServer envoyextproc.ExternalProcessor_ProcessServer) error {
	var (
		ctx            = processServer.Context()
		currentRequest message_processor.RequestState
	)

	// Ensure cleanup on exit
	defer func() {
		s.requestCounter.Add(1)

		if currentRequest.Ongoing {
			if !currentRequest.AwaitingResponseBody {
				instr.Logger().Warn("external_processing: stream stopped during a request, making sure the current span is closed\n")
			}
			currentRequest.Close()
		}
	}()

	for {
		if err := s.checkContext(ctx); err != nil {
			return err
		}

		var processingRequest envoyextproc.ProcessingRequest
		if err := processServer.RecvMsg(&processingRequest); err != nil {
			return s.handleReceiveError(err)
		}

		// Process the message
		action, err := s.processMessage(ctx, &processingRequest, &currentRequest)
		if err != nil {
			instr.Logger().Error("external_processing: error processing request: %s\n", err.Error())
			return err
		}

		// Handle the action returned by the message processor
		processingResponse, err := s.handleAction(action, &processingRequest)
		if err != nil {
			instr.Logger().Error("external_processing: error handling action: %s\n", err.Error())
			return err
		}

		if processingResponse == nil {
			instr.Logger().Debug("external_processing: end of stream reached")
			return nil
		}

		if err := s.sendResponse(processServer, processingResponse); err != nil {
			return err
		}

		if _, ok := processingResponse.Response.(*envoyextproc.ProcessingResponse_ImmediateResponse); ok {
			instr.Logger().Debug("external_processing: request blocked, end the stream")
			return nil
		}
	}
}

// checkContext checks if the context has been cancelled or other if there's an unexpected error
func (s *appsecEnvoyExternalProcessorServer) checkContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil
		}
		return ctx.Err()
	default:
		return nil
	}
}

// handleReceiveError handles errors when receiving messages
func (s *appsecEnvoyExternalProcessorServer) handleReceiveError(err error) error {
	if st, ok := status.FromError(err); (ok && st.Code() == codes.Canceled) || err == io.EOF {
		return nil
	}

	instr.Logger().Error("external_processing: error receiving request/response: %s\n", err.Error())
	return status.Errorf(codes.Unknown, "Error receiving request/response: %s", err.Error())
}

// processMessage processes a single message based on its type
func (s *appsecEnvoyExternalProcessorServer) processMessage(ctx context.Context, req *envoyextproc.ProcessingRequest, currentRequest *message_processor.RequestState) (message_processor.Action, error) {
	switch v := req.Request.(type) {
	case *envoyextproc.ProcessingRequest_RequestHeaders:
		var (
			action message_processor.Action
			err    error
		)
		*currentRequest, action, err = s.messageProcessor.OnRequestHeaders(ctx, &requestHeadersEnvoy{v, s.config.Integration})
		return action, err

	case *envoyextproc.ProcessingRequest_RequestBody:
		if !currentRequest.Ongoing {
			return nil, status.Errorf(codes.InvalidArgument, "Received request body without request headers")
		}
		return s.messageProcessor.OnRequestBody(&requestBodyEnvoy{v}, *currentRequest)

	case *envoyextproc.ProcessingRequest_ResponseHeaders:
		if !currentRequest.Ongoing {
			// Handle case where request headers were never sent
			instr.Logger().Warn("external_processing: can't process the response: envoy never sent the beginning of the request, this is a known issue" +
				" and can happen when a malformed request is sent to Envoy where the header Host is missing. See link to issue https://github.com/envoyproxy/envoy/issues/38022")
			return nil, status.Errorf(codes.InvalidArgument, "Error processing response headers from ext_proc: can't process the response")
		}
		return s.messageProcessor.OnResponseHeaders(&responseHeadersEnvoy{v}, *currentRequest)

	case *envoyextproc.ProcessingRequest_ResponseBody:
		if !currentRequest.Ongoing {
			return nil, status.Errorf(codes.InvalidArgument, "Received response body without request context")
		}
		return s.messageProcessor.OnResponseBody(&responseBodyEnvoy{v}, *currentRequest)

	case *envoyextproc.ProcessingRequest_RequestTrailers:
		instr.Logger().Debug("external_processing: received unexpected message of type RequestTrailers, ignoring it. " +
			"Please make sure your Envoy configuration does not include RequestTrailer to accelerate processing.")
		return s.messageProcessor.OnRequestTrailers(message_processor.RequestState{})

	case *envoyextproc.ProcessingRequest_ResponseTrailers:
		instr.Logger().Debug("external_processing: received unexpected message of type ResponseTrailers, ignoring it. " +
			"Please make sure your Envoy configuration does not include RequestTrailer to accelerate processing.")
		return s.messageProcessor.OnResponseTrailers(message_processor.RequestState{})

	default:
		return nil, status.Errorf(codes.Unknown, "Unknown request type: %T", v)
	}
}

// handleAction handles the action returned by the message processor
func (s *appsecEnvoyExternalProcessorServer) handleAction(action message_processor.Action, req *envoyextproc.ProcessingRequest) (*envoyextproc.ProcessingResponse, error) {
	switch action.Type() {
	case message_processor.ActionTypeContinue:
		if action.Response() == nil {
			return getProcessingResponse(req, nil)
		}

		if data := action.Response().(*message_processor.HeadersResponseData); data != nil {
			return buildHeadersResponse(data), nil
		}

		// Could happen if a new response type with data is implemented, and we forget to handle it here.
		// However, at the moment, we only have HeadersResponseData as a response type for ActionTypeContinue
		return nil, status.Errorf(codes.Unknown, "Unknown action data type: %T for ActionTypeContinue", action.Response())
	case message_processor.ActionTypeBlock:
		data := action.Response().(*message_processor.BlockResponseData)
		return buildImmediateResponse(data), nil
	case message_processor.ActionTypeFinish:
		return nil, nil
	}
	return nil, status.Errorf(codes.Unknown, "Unknown action type: %v", action.Type())
}

func getProcessingResponse(req *envoyextproc.ProcessingRequest, commonResponse *envoyextproc.CommonResponse) (*envoyextproc.ProcessingResponse, error) {
	response := &envoyextproc.ProcessingResponse{}
	var err error

	switch v := req.Request.(type) {
	case *envoyextproc.ProcessingRequest_RequestHeaders:
		response.Response = &envoyextproc.ProcessingResponse_RequestHeaders{RequestHeaders: &envoyextproc.HeadersResponse{Response: commonResponse}}
	case *envoyextproc.ProcessingRequest_RequestBody:
		response.Response = &envoyextproc.ProcessingResponse_RequestBody{RequestBody: &envoyextproc.BodyResponse{Response: commonResponse}}
	case *envoyextproc.ProcessingRequest_ResponseHeaders:
		response.Response = &envoyextproc.ProcessingResponse_ResponseHeaders{ResponseHeaders: &envoyextproc.HeadersResponse{Response: commonResponse}}
	case *envoyextproc.ProcessingRequest_ResponseBody:
		response.Response = &envoyextproc.ProcessingResponse_ResponseBody{ResponseBody: &envoyextproc.BodyResponse{Response: commonResponse}}
	case *envoyextproc.ProcessingRequest_RequestTrailers:
		response.Response = &envoyextproc.ProcessingResponse_RequestTrailers{}
	case *envoyextproc.ProcessingRequest_ResponseTrailers:
		response.Response = &envoyextproc.ProcessingResponse_ResponseTrailers{}
	default:
		err = status.Errorf(codes.Unknown, "Unknown request type: %T", v)
	}

	return response, err
}

// sendResponse sends a processing response back to Envoy
func (s *appsecEnvoyExternalProcessorServer) sendResponse(processServer envoyextproc.ExternalProcessor_ProcessServer, response *envoyextproc.ProcessingResponse) error {
	instr.Logger().Debug("external_processing: sending response: %v\n", response)

	if err := processServer.SendMsg(response); err != nil {
		instr.Logger().Error("external_processing: error sending response (probably because of an Envoy timeout): %s", err.Error())
		return status.Errorf(codes.Unknown, "Error sending response (probably because of an Envoy timeout): %s", err.Error())
	}

	return nil
}
