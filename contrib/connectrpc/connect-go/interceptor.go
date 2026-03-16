// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"context"
	"fmt"

	"connectrpc.com/connect"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// NewInterceptor returns a new Connect interceptor that traces unary and streaming RPCs.
// It handles both client-side and server-side interception using connect.Spec.IsClient.
func NewInterceptor(opts ...Option) connect.Interceptor {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn.apply(cfg)
	}
	instr.Logger().Debug("contrib/connectrpc/connect-go: Configuring Interceptor: %#v", cfg)
	return &interceptor{cfg: cfg}
}

type interceptor struct {
	cfg *config
}

func (i *interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		procedure := req.Spec().Procedure
		if _, ok := i.cfg.untracedMethods[procedure]; ok {
			return next(ctx, req)
		}

		isClient := req.Spec().IsClient
		service, method := parseProcedure(procedure)
		kind := methodKindFromStreamType(req.Spec().StreamType)

		var component instrumentation.Component
		var spanKind string
		if isClient {
			component = instrumentation.ComponentClient
			spanKind = ext.SpanKindClient
		} else {
			component = instrumentation.ComponentServer
			spanKind = ext.SpanKindServer
		}

		opName := i.cfg.operationNameForComponent(component)
		svcName := i.cfg.serviceNameForComponent(component)

		spanOpts := i.cfg.startSpanOptions(
			tracer.ServiceName(svcName),
			tracer.ResourceName(procedure),
			tracer.Tag(tagMethodName, procedure),
			tracer.Tag(tagMethodKind, kind),
			spanTypeRPC,
			tracer.Tag(ext.Component, componentName),
			tracer.Tag(ext.SpanKind, spanKind),
			tracer.Tag(ext.RPCSystem, ext.RPCSystemConnectRPC),
			tracer.Tag(ext.RPCService, service),
			tracer.Tag(ext.RPCMethod, method),
			tracer.Measured(),
		)

		if isClient {
			// Client: inject trace context into outgoing headers
			span, ctx := tracer.StartSpanFromContext(ctx, opName, spanOpts...)
			if addr := req.Peer().Addr; addr != "" {
				span.SetTag(ext.PeerHostname, addr)
			}
			if err := tracer.Inject(span.Context(), tracer.HTTPHeadersCarrier(req.Header())); err != nil {
				instr.Logger().Warn("contrib/connectrpc/connect-go: failed to inject trace context: %s", err.Error())
			}
			setHeaderTags(req.Header(), i.cfg, span)
			resp, err := next(ctx, req)
			finishWithError(span, err, i.cfg)
			return resp, err
		}

		// Server: extract trace context from incoming headers
		sctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(req.Header()))
		if err == nil {
			if sctx != nil && sctx.SpanLinks() != nil {
				spanOpts = append(spanOpts, tracer.WithSpanLinks(sctx.SpanLinks()))
			}
			spanOpts = append(spanOpts, tracer.ChildOf(sctx))
		}
		span, ctx := tracer.StartSpanFromContext(ctx, opName, spanOpts...)
		setHeaderTags(req.Header(), i.cfg, span)
		if i.cfg.withRequestTags {
			if msg := req.Any(); msg != nil {
				span.SetTag(tagRequest, fmt.Sprintf("%v", msg))
			}
		}
		resp, rpcErr := next(ctx, req)
		finishWithError(span, rpcErr, i.cfg)
		return resp, rpcErr
	}
}

func (i *interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		procedure := spec.Procedure
		if _, ok := i.cfg.untracedMethods[procedure]; ok {
			return next(ctx, spec)
		}

		service, method := parseProcedure(procedure)
		kind := methodKindFromStreamType(spec.StreamType)
		opName := i.cfg.operationNameForComponent(instrumentation.ComponentClient)
		svcName := i.cfg.serviceNameForComponent(instrumentation.ComponentClient)

		var callSpan *tracer.Span
		if i.cfg.traceStreamCalls {
			spanOpts := i.cfg.startSpanOptions(
				tracer.ServiceName(svcName),
				tracer.ResourceName(procedure),
				tracer.Tag(tagMethodName, procedure),
				tracer.Tag(tagMethodKind, kind),
				spanTypeRPC,
				tracer.Tag(ext.Component, componentName),
				tracer.Tag(ext.SpanKind, ext.SpanKindClient),
				tracer.Tag(ext.RPCSystem, ext.RPCSystemConnectRPC),
				tracer.Tag(ext.RPCService, service),
				tracer.Tag(ext.RPCMethod, method),
				tracer.Measured(),
			)
			callSpan, ctx = tracer.StartSpanFromContext(ctx, opName, spanOpts...)
		}

		conn := next(ctx, spec)

		// Inject trace context into outgoing headers
		if span, ok := tracer.SpanFromContext(ctx); ok {
			if err := tracer.Inject(span.Context(), tracer.HTTPHeadersCarrier(conn.RequestHeader())); err != nil {
				instr.Logger().Warn("contrib/connectrpc/connect-go: failed to inject trace context: %s", err.Error())
			}
		}

		return &tracedStreamingClientConn{
			StreamingClientConn: conn,
			cfg:                 i.cfg,
			ctx:                 ctx,
			procedure:           procedure,
			callSpan:            callSpan,
		}
	}
}

func (i *interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		procedure := conn.Spec().Procedure
		if _, ok := i.cfg.untracedMethods[procedure]; ok {
			return next(ctx, conn)
		}

		service, method := parseProcedure(procedure)
		kind := methodKindFromStreamType(conn.Spec().StreamType)
		opName := i.cfg.operationNameForComponent(instrumentation.ComponentServer)
		svcName := i.cfg.serviceNameForComponent(instrumentation.ComponentServer)

		spanOpts := i.cfg.startSpanOptions(
			tracer.ServiceName(svcName),
			tracer.ResourceName(procedure),
			tracer.Tag(tagMethodName, procedure),
			tracer.Tag(tagMethodKind, kind),
			spanTypeRPC,
			tracer.Tag(ext.Component, componentName),
			tracer.Tag(ext.SpanKind, ext.SpanKindServer),
			tracer.Tag(ext.RPCSystem, ext.RPCSystemConnectRPC),
			tracer.Tag(ext.RPCService, service),
			tracer.Tag(ext.RPCMethod, method),
			tracer.Measured(),
		)

		// Extract trace context from incoming headers
		sctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(conn.RequestHeader()))
		if err == nil {
			if sctx != nil && sctx.SpanLinks() != nil {
				spanOpts = append(spanOpts, tracer.WithSpanLinks(sctx.SpanLinks()))
			}
			spanOpts = append(spanOpts, tracer.ChildOf(sctx))
		}

		var callSpan *tracer.Span
		if i.cfg.traceStreamCalls {
			callSpan, ctx = tracer.StartSpanFromContext(ctx, opName, spanOpts...)
			setHeaderTags(conn.RequestHeader(), i.cfg, callSpan)
		}

		rpcErr := next(ctx, &tracedStreamingHandlerConn{
			StreamingHandlerConn: conn,
			cfg:                  i.cfg,
			ctx:                  ctx,
			procedure:            procedure,
		})

		if callSpan != nil {
			finishWithError(callSpan, rpcErr, i.cfg)
		}
		return rpcErr
	}
}

// tracedStreamingClientConn wraps a connect.StreamingClientConn to trace Send and Receive calls.
type tracedStreamingClientConn struct {
	connect.StreamingClientConn
	cfg       *config
	ctx       context.Context
	procedure string
	callSpan  *tracer.Span
}

func (c *tracedStreamingClientConn) Send(msg any) error {
	err := c.StreamingClientConn.Send(msg)
	if c.cfg.traceStreamMessages {
		span, _ := tracer.StartSpanFromContext(
			c.ctx,
			"connect.message",
			c.cfg.startSpanOptions(
				tracer.ServiceName(c.cfg.serviceNameForComponent(instrumentation.ComponentClient)),
				tracer.ResourceName(c.procedure),
				tracer.Tag(tagMethodName, c.procedure),
				spanTypeRPC,
				tracer.Tag(ext.Component, componentName),
				tracer.Tag(ext.SpanKind, ext.SpanKindClient),
			)...,
		)
		finishWithError(span, err, c.cfg)
	}
	return err
}

func (c *tracedStreamingClientConn) Receive(msg any) error {
	err := c.StreamingClientConn.Receive(msg)
	if c.cfg.traceStreamMessages {
		span, _ := tracer.StartSpanFromContext(
			c.ctx,
			"connect.message",
			c.cfg.startSpanOptions(
				tracer.ServiceName(c.cfg.serviceNameForComponent(instrumentation.ComponentClient)),
				tracer.ResourceName(c.procedure),
				tracer.Tag(tagMethodName, c.procedure),
				spanTypeRPC,
				tracer.Tag(ext.Component, componentName),
				tracer.Tag(ext.SpanKind, ext.SpanKindClient),
			)...,
		)
		finishWithError(span, err, c.cfg)
	}
	return err
}

func (c *tracedStreamingClientConn) CloseResponse() error {
	err := c.StreamingClientConn.CloseResponse()
	if c.callSpan != nil {
		finishWithError(c.callSpan, err, c.cfg)
	}
	return err
}

// tracedStreamingHandlerConn wraps a connect.StreamingHandlerConn to trace Send and Receive calls.
type tracedStreamingHandlerConn struct {
	connect.StreamingHandlerConn
	cfg       *config
	ctx       context.Context
	procedure string
}

func (c *tracedStreamingHandlerConn) Send(msg any) error {
	err := c.StreamingHandlerConn.Send(msg)
	if c.cfg.traceStreamMessages {
		span, _ := tracer.StartSpanFromContext(
			c.ctx,
			"connect.message",
			c.cfg.startSpanOptions(
				tracer.ServiceName(c.cfg.serviceNameForComponent(instrumentation.ComponentServer)),
				tracer.ResourceName(c.procedure),
				tracer.Tag(tagMethodName, c.procedure),
				spanTypeRPC,
				tracer.Tag(ext.Component, componentName),
				tracer.Tag(ext.SpanKind, ext.SpanKindServer),
			)...,
		)
		finishWithError(span, err, c.cfg)
	}
	return err
}

func (c *tracedStreamingHandlerConn) Receive(msg any) error {
	err := c.StreamingHandlerConn.Receive(msg)
	if c.cfg.traceStreamMessages {
		span, _ := tracer.StartSpanFromContext(
			c.ctx,
			"connect.message",
			c.cfg.startSpanOptions(
				tracer.ServiceName(c.cfg.serviceNameForComponent(instrumentation.ComponentServer)),
				tracer.ResourceName(c.procedure),
				tracer.Tag(tagMethodName, c.procedure),
				spanTypeRPC,
				tracer.Tag(ext.Component, componentName),
				tracer.Tag(ext.SpanKind, ext.SpanKindServer),
			)...,
		)
		finishWithError(span, err, c.cfg)
	}
	return err
}
