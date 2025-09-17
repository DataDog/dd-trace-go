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
	"github.com/negasus/haproxy-spoe-go/message"
	"github.com/negasus/haproxy-spoe-go/request"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageHAProxyStreamProcessingOffload)
}

type HAProxySPOA struct {
	requestStateCache *ttlcache.Cache[uint64, *proxy.RequestState]
	messageProcessor  proxy.Processor
}

type AppsecHAProxyConfig struct {
	Context              context.Context
	BlockingUnavailable  bool
	BodyParsingSizeLimit int
}

func NewHAProxySPOA(config AppsecHAProxyConfig) *HAProxySPOA {
	spoa := &HAProxySPOA{
		messageProcessor: proxy.NewProcessor(proxy.ProcessorConfig{
			BlockingUnavailable:  config.BlockingUnavailable,
			BodyParsingSizeLimit: config.BodyParsingSizeLimit,
			Framework:            "github.com/haproxy/haproxy",
			Context:              config.Context,
			ContinueMessageFunc:  continueActionFunc,
			BlockMessageFunc:     blockActionFunc,
		}, instr),
	}

	spoa.requestStateCache = initRequestStateCache(func(rs *proxy.RequestState) {
		if rs.State.Ongoing() {
			instr.Logger().Warn("haproxy_spoa: stream stopped during a request, making sure the current span is closed\n")
			_ = rs.Close()
		}
	})

	if config.BodyParsingSizeLimit <= 0 {
		instr.Logger().Info("haproxy_spoa: body parsing size limit set to 0 or negative. The request and response bodies will be ignored.")
	}

	return spoa
}

type haproxyContextRequestDataType struct {
	req     *request.Request
	msg     *message.Message
	timeout string
}

var haproxyRequestKey haproxyContextRequestDataType

// Handler processes SPOE requests from HAProxy
func (s *HAProxySPOA) Handler(req *request.Request) {
	instr.Logger().Debug("haproxy_spoa: handle request EngineID: '%s', StreamID: '%d', FrameID: '%d' with %d messages", req.EngineID, req.StreamID, req.FrameID, req.Messages.Len())

	// Process each message
	for i := 0; i < req.Messages.Len(); i++ {
		msg, err := req.Messages.GetByIndex(i)
		if err != nil {
			instr.Logger().Warn("haproxy_spoa: failed to get message at index %d: %v", i, err)
			continue
		}

		reqState, _ := getCurrentRequest(s.requestStateCache, msg)

		err = s.processMessage(req, msg, reqState)
		if err != nil && err != io.EOF {
			instr.Logger().Error("haproxy_spoa: error processing message %s: %v", msg.Name, err)
			return
		}
	}
}

func (s *HAProxySPOA) processMessage(req *request.Request, msg *message.Message, currentRequest *proxy.RequestState) error {
	instr.Logger().Debug("haproxy_spoa: handling message: %s", msg.Name)

	requestContextData := &haproxyContextRequestDataType{req: req, msg: msg}

	switch msg.Name {
	case "http-request-headers-msg":
		ctx := context.WithValue(context.Background(), haproxyRequestKey, requestContextData)
		requestState, err := s.messageProcessor.OnRequestHeaders(ctx, &messageRequestHeaders{req: req, msg: msg})
		if err != nil {
			return err
		}
		return s.cacheRequest(requestState, msg)

	case "http-request-body-msg":
		ctx := currentRequest.Context
		currentRequest.Context = context.WithValue(ctx, haproxyRequestKey, requestContextData)
		err := s.messageProcessor.OnRequestBody(&messageBody{msg: msg}, currentRequest)
		currentRequest.Context = ctx
		return err

	case "http-response-headers-msg":
		ctx := currentRequest.Context
		currentRequest.Context = context.WithValue(ctx, haproxyRequestKey, requestContextData)
		err := s.messageProcessor.OnResponseHeaders(&responseHeadersHAProxy{msg: msg}, currentRequest)
		currentRequest.Context = ctx
		return err

	case "http-response-body-msg":
		currentRequest.Context = context.WithValue(currentRequest.Context, haproxyRequestKey, requestContextData)
		return s.messageProcessor.OnResponseBody(&messageBody{msg: msg}, currentRequest)

	default:
		return fmt.Errorf("unknown message type: %s", msg.Name)
	}
}

func (s *HAProxySPOA) cacheRequest(reqState proxy.RequestState, msg *message.Message) error {
	timeout := getStringValue(msg, "timeout")

	span, ok := reqState.Span()
	if !ok {
		return fmt.Errorf("failed to retreive the span from the context of the request")
	}

	spanId := span.Context().SpanID()
	storeRequestState(s.requestStateCache, spanId, reqState, timeout)

	return nil
}
