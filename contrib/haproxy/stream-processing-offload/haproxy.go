// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package streamprocessingoffload

import (
	"context"
	"fmt"
	"io"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/proxy"

	"github.com/jellydator/ttlcache/v3"
	"github.com/negasus/haproxy-spoe-go/request"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageHAProxyStreamProcessingOffload)
}

// Logger returns the integration logger for the HAProxy Stream Processing Offload package
func Logger() instrumentation.Logger {
	return instr.Logger()
}

// HAProxySPOA defines the AppSec HAProxy Stream Processing Offload Agent
type HAProxySPOA struct {
	requestStateCache *ttlcache.Cache[uint64, *proxy.RequestState]
	messageProcessor  proxy.Processor
}

// AppsecHAProxyConfig contains configuration for the AppSec HAProxy Stream Processing Offload Agent
type AppsecHAProxyConfig struct {
	Context              context.Context
	BlockingUnavailable  bool
	BodyParsingSizeLimit int
}

// NewHAProxySPOA creates a new AppSec HAProxy Stream Processing Offload Agent
func NewHAProxySPOA(config AppsecHAProxyConfig) *HAProxySPOA {
	return &HAProxySPOA{
		messageProcessor: proxy.NewProcessor(proxy.ProcessorConfig{
			BlockingUnavailable:  config.BlockingUnavailable,
			BodyParsingSizeLimit: &config.BodyParsingSizeLimit,
			Framework:            "haproxy/haproxy",
			Context:              config.Context,
			ContinueMessageFunc:  continueActionFunc,
			BlockMessageFunc:     blockActionFunc,
		}, instr),
		requestStateCache: initRequestStateCache(func(rs *proxy.RequestState) {
			if rs.State.Ongoing() {
				instr.Logger().Warn("haproxy_spoa: backend server timeout reached, closing the span for the request.\n")
				_ = rs.Close()
			}
		}),
	}
}

type haproxyRequestContextKey struct{}

type haproxyRequestContextData struct {
	req     *request.Request
	msg     *haproxyMessage
	timeout string
}

// Handler processes Stream Processing Offload messages from HAProxy
func (s *HAProxySPOA) Handler(req *request.Request) {
	instr.Logger().Debug("haproxy_spoa: handle request EngineID: '%s', StreamID: '%d', FrameID: '%d' with %d messages", req.EngineID, req.StreamID, req.FrameID, req.Messages.Len())

	// Process each message
	for i := 0; i < req.Messages.Len(); i++ {
		msg, err := req.Messages.GetByIndex(i)
		if err != nil {
			instr.Logger().Warn("haproxy_spoa: failed to get message at index %d: %v", i, err)
			continue
		}

		hMsg := newHaproxyMessage(msg)
		reqState, _ := getCurrentRequest(s.requestStateCache, hMsg)

		err = s.processMessage(req, hMsg, reqState)
		if err != nil && err != io.EOF {
			instr.Logger().Error("haproxy_spoa: error processing message %s: %v", msg.Name, err)
			return
		}
	}
}

// processMessage processes a single message from HAProxy based on its name.
func (s *HAProxySPOA) processMessage(req *request.Request, msg *haproxyMessage, currentRequest *proxy.RequestState) error {
	instr.Logger().Debug("haproxy_spoa: handling message: %s", msg.Name)

	requestContextData := &haproxyRequestContextData{req: req, msg: msg}

	switch msg.Name {
	case MessageHTTPRequestHeaders:
		ctx := context.WithValue(context.Background(), haproxyRequestContextKey{}, requestContextData)
		requestState, err := s.messageProcessor.OnRequestHeaders(ctx, &messageRequestHeaders{req: req, msg: msg})
		if err != nil {
			return err
		}
		return s.cacheRequest(requestState, msg)

	case MessageHTTPRequestBody:
		if currentRequest == nil {
			return fmt.Errorf("received request body outside of a started a request")
		}

		ctx := currentRequest.Context
		currentRequest.Context = context.WithValue(ctx, haproxyRequestContextKey{}, requestContextData)
		err := s.messageProcessor.OnRequestBody(&messageBody{msg: msg}, currentRequest)
		currentRequest.Context = ctx
		return err

	case MessageHTTPResponseHeaders:
		if currentRequest == nil {
			return fmt.Errorf("received reponse headers outside of a started a request")
		}

		ctx := currentRequest.Context
		currentRequest.Context = context.WithValue(ctx, haproxyRequestContextKey{}, requestContextData)
		err := s.messageProcessor.OnResponseHeaders(&responseHeadersHAProxy{msg: msg}, currentRequest)
		currentRequest.Context = ctx
		return err

	case MessageHTTPResponseBody:
		if currentRequest == nil {
			return fmt.Errorf("received reponse body outside of a started a request")
		}

		currentRequest.Context = context.WithValue(currentRequest.Context, haproxyRequestContextKey{}, requestContextData)
		return s.messageProcessor.OnResponseBody(&messageBody{msg: msg}, currentRequest)

	default:
		return fmt.Errorf("unknown message name: %s", msg.Name)
	}
}

// cacheRequest stores the request state in the cache based on the `span_id` extracted from the message.
func (s *HAProxySPOA) cacheRequest(reqState proxy.RequestState, msg *haproxyMessage) error {
	timeout := msg.String("timeout")

	span, ok := reqState.Span()
	if !ok {
		return fmt.Errorf("failed to retreive the span from the context of the request")
	}

	spanId := span.Context().SpanID()
	storeRequestState(s.requestStateCache, spanId, reqState, timeout)

	return nil
}
