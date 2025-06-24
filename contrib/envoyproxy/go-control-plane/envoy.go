// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gocontrolplane

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	envoyextproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

const componentNameEnvoy = "envoyproxy/go-control-plane"
const componentNameGCPServiceExtension = "gcp-service-extension"

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageEnvoyProxyGoControlPlane)
}

// AppsecEnvoyConfig contains configuration for the AppSec Envoy processor
type AppsecEnvoyConfig struct {
	IsGCPServiceExtension bool
	BlockingUnavailable   bool
	Context               context.Context
	BodyParsingSizeLimit  int
}

// appsecEnvoyExternalProcessorServer is a server that implements the Envoy ExternalProcessorServer interface.
type appsecEnvoyExternalProcessorServer struct {
	envoyextproc.ExternalProcessorServer
	config           AppsecEnvoyConfig
	requestCounter   atomic.Uint32
	messageProcessor *messageProcessor
}

// AppsecEnvoyExternalProcessorServer creates a new external processor server with AAP enabled
func AppsecEnvoyExternalProcessorServer(userImplementation envoyextproc.ExternalProcessorServer, config AppsecEnvoyConfig) envoyextproc.ExternalProcessorServer {
	processor := &appsecEnvoyExternalProcessorServer{
		ExternalProcessorServer: userImplementation,
		config:                  config,
		messageProcessor:        newMessageProcessor(config),
	}

	if config.Context != nil {
		processor.startMetricsReporter(config.Context)
	}

	if config.BodyParsingSizeLimit <= 0 {
		instr.Logger().Info("external_processing: body parsing size limit set to 0 or negative. The body of requests and responses will not be analyzed.")
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
	ctx := processServer.Context()
	var currentRequest *requestState

	// Ensure cleanup on exit
	defer func() {
		s.requestCounter.Add(1)

		if currentRequest != nil && !currentRequest.IsComplete {
			if !currentRequest.AwaitingResponseBody {
				instr.Logger().Warn("external_processing: stream stopped during a request, making sure the current span is closed\n")
			}
			currentRequest.Complete()
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
		processingResponse, err := s.processMessage(ctx, &processingRequest, &currentRequest)
		if err != nil {
			instr.Logger().Error("external_processing: error processing request: %v\n", err)
			return err
		}

		if processingResponse == nil {
			instr.Logger().Debug("external_processing: end of stream reached")
			return nil
		}

		if err := s.sendResponse(processServer, processingResponse); err != nil {
			return err
		}

		if currentRequest != nil && currentRequest.Blocked {
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

	instr.Logger().Warn("external_processing: error receiving request/response: %v\n", err)
	return status.Errorf(codes.Unknown, "Error receiving request/response: %v", err)
}

// processMessage processes a single message based on its type
func (s *appsecEnvoyExternalProcessorServer) processMessage(ctx context.Context, req *envoyextproc.ProcessingRequest, currentRequest **requestState) (*envoyextproc.ProcessingResponse, error) {
	switch v := req.Request.(type) {
	case *envoyextproc.ProcessingRequest_RequestHeaders:
		response, state, err := s.messageProcessor.ProcessRequestHeaders(ctx, v)
		*currentRequest = state
		return response, err

	case *envoyextproc.ProcessingRequest_RequestBody:
		if *currentRequest == nil {
			return nil, status.Errorf(codes.InvalidArgument, "Received request body without request headers")
		}
		return s.messageProcessor.ProcessRequestBody(v, *currentRequest), nil

	case *envoyextproc.ProcessingRequest_ResponseHeaders:
		if *currentRequest == nil {
			// Handle case where request headers were never sent
			instr.Logger().Warn("external_processing: can't process the response: envoy never sent the beginning of the request, this is a known issue" +
				" and can happen when a malformed request is sent to Envoy where the header Host is missing. See link to issue https://github.com/envoyproxy/envoy/issues/38022")
			return nil, status.Errorf(codes.InvalidArgument, "Error processing response headers from ext_proc: can't process the response")
		}
		return s.messageProcessor.ProcessResponseHeaders(v, *currentRequest)

	case *envoyextproc.ProcessingRequest_ResponseBody:
		if *currentRequest == nil {
			return nil, status.Errorf(codes.InvalidArgument, "Received response body without request context")
		}
		return s.messageProcessor.ProcessResponseBody(v, *currentRequest), nil

	case *envoyextproc.ProcessingRequest_RequestTrailers:
		return &envoyextproc.ProcessingResponse{
			Response: &envoyextproc.ProcessingResponse_RequestTrailers{},
		}, nil

	case *envoyextproc.ProcessingRequest_ResponseTrailers:
		return &envoyextproc.ProcessingResponse{
			Response: &envoyextproc.ProcessingResponse_ResponseTrailers{},
		}, nil

	default:
		return nil, status.Errorf(codes.Unknown, "Unknown request type: %T", v)
	}
}

// sendResponse sends a processing response back to Envoy
func (s *appsecEnvoyExternalProcessorServer) sendResponse(processServer envoyextproc.ExternalProcessor_ProcessServer, response *envoyextproc.ProcessingResponse) error {
	instr.Logger().Debug("external_processing: sending response: %v\n", response)

	if err := processServer.SendMsg(response); err != nil {
		instr.Logger().Warn("external_processing: error sending response (probably because of an Envoy timeout): %v", err)
		return status.Errorf(codes.Unknown, "Error sending response (probably because of an Envoy timeout): %v", err)
	}

	return nil
}

// isBodySupported checks if the body should be analyzed based on content type
func isBodySupported(contentType string, config AppsecEnvoyConfig) bool {
	if config.BodyParsingSizeLimit <= 0 {
		return false
	}

	// Check if content type is a JSON type
	values := strings.Split(contentType, ";")
	for _, v := range values {
		if strings.HasSuffix(strings.TrimSpace(v), "json") {
			return true
		}
	}

	return false
}
