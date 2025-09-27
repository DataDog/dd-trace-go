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

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/proxy"

	envoyextproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageEnvoyProxyGoControlPlane)
}

// Integration represents the proxy integration type that is used for the External Processing.
type Integration int

const (
	_ Integration = iota
	GCPServiceExtensionIntegration
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
	messageProcessor proxy.Processor
}

// AppsecEnvoyExternalProcessorServer creates a new external processor server with AAP enabled
func AppsecEnvoyExternalProcessorServer(userImplementation envoyextproc.ExternalProcessorServer, config AppsecEnvoyConfig) envoyextproc.ExternalProcessorServer {
	processor := &appsecEnvoyExternalProcessorServer{
		ExternalProcessorServer: userImplementation,
		config:                  config,
		messageProcessor: proxy.NewProcessor(proxy.ProcessorConfig{
			BlockingUnavailable:  config.BlockingUnavailable,
			BodyParsingSizeLimit: config.BodyParsingSizeLimit,
			Framework:            "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3",
			Context:              config.Context,
			ContinueMessageFunc:  continueActionFunc,
			BlockMessageFunc:     blockActionFunc,
		}, instr),
	}

	switch config.Integration {
	case GCPServiceExtensionIntegration, EnvoyIntegration, IstioIntegration:
	default:
		instr.Logger().Error("external_processing: invalid proxy integration type %d. Defaulting to GCPServiceExtensionIntegration", config.Integration)
		config.Integration = GCPServiceExtensionIntegration
	}

	return processor
}

type processServerKeyType struct{}

var processServerKey processServerKeyType

// Process handles the bidirectional stream that Envoy uses to control the filter
func (s *appsecEnvoyExternalProcessorServer) Process(processServer envoyextproc.ExternalProcessor_ProcessServer) error {
	var (
		ctx            = context.WithValue(processServer.Context(), processServerKey, processServer)
		currentRequest proxy.RequestState
	)

	// Ensure cleanup on exit
	defer func() {
		if currentRequest.State.Ongoing() {
			instr.Logger().Warn("external_processing: stream stopped during a request, making sure the current span is closed\n")
			currentRequest.Close()
		}
	}()

	for {
		if err := s.checkContext(processServer.Context()); err != nil {
			return err
		}

		var processingRequest envoyextproc.ProcessingRequest
		if err := processServer.RecvMsg(&processingRequest); err != nil {
			return s.handleReceiveError(err)
		}

		// Process the message
		err := s.processMessage(ctx, &processingRequest, &currentRequest)
		if err != nil && err != io.EOF {
			instr.Logger().Error("external_processing: error processing request: %s\n", err.Error())
			return err
		}

		if err == io.EOF {
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
func (s *appsecEnvoyExternalProcessorServer) processMessage(ctx context.Context, req *envoyextproc.ProcessingRequest, currentRequest *proxy.RequestState) (err error) {
	switch v := req.Request.(type) {
	case *envoyextproc.ProcessingRequest_RequestHeaders:
		*currentRequest, err = s.messageProcessor.OnRequestHeaders(ctx, &messageRequestHeaders{ProcessingRequest: req, HttpHeaders: req.GetRequestHeaders(), integration: s.config.Integration})
		return err

	case *envoyextproc.ProcessingRequest_RequestBody:
		return s.messageProcessor.OnRequestBody(&messageBody{ProcessingRequest: req, HttpBody: req.GetRequestBody()}, currentRequest)

	case *envoyextproc.ProcessingRequest_ResponseHeaders:
		if !currentRequest.State.Ongoing() {
			// Handle case where request headers were never sent
			instr.Logger().Warn("external_processing: can't process the response: envoy never sent the beginning of the request, this is a known issue" +
				" and can happen when a malformed request is sent to Envoy where the header Host is missing. See link to issue https://github.com/envoyproxy/envoy/issues/38022")
			return status.Errorf(codes.InvalidArgument, "Error processing response headers from ext_proc: can't process the response")
		}
		return s.messageProcessor.OnResponseHeaders(&responseHeadersEnvoy{ProcessingRequest: req, HttpHeaders: req.GetResponseHeaders()}, currentRequest)

	case *envoyextproc.ProcessingRequest_ResponseBody:
		return s.messageProcessor.OnResponseBody(&messageBody{ProcessingRequest: req, HttpBody: req.GetResponseBody()}, currentRequest)

	case *envoyextproc.ProcessingRequest_RequestTrailers:
		return s.messageProcessor.OnRequestTrailers(currentRequest)

	case *envoyextproc.ProcessingRequest_ResponseTrailers:
		return s.messageProcessor.OnResponseTrailers(currentRequest)

	default:
		return status.Errorf(codes.Unknown, "Unknown request type: %T", v)
	}
}
