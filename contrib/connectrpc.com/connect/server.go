// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"context"
	"maps"
	"net/http"

	connectrpc "connectrpc.com/connect"

	"github.com/DataDog/dd-trace-go/v2/contrib/connectrpc.com/connectshared"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

func (cfg *config) traceUnaryHandler(ctx context.Context, spec connectrpc.Spec, request connectrpc.AnyRequest, next connectrpc.UnaryFunc) (response connectrpc.AnyResponse, err error) {
	protocol := connectshared.ProtocolFor(request.Peer().Protocol)
	httpMethod := request.HTTPMethod()
	header := request.Header()
	span, ctx := cfg.core.StartServerCall(ctx, sharedSpec(spec), protocol, request, tracer.HTTPHeadersCarrier(header))
	finish := connectshared.SpanFinish{Procedure: spec.Procedure, Protocol: protocol, Mode: connectshared.UnaryFinishMode(protocol, httpMethod, true)}
	var guard connectshared.Guard
	defer cfg.core.FinishOnReturn(&guard, &span, &err, &finish, nil)
	guard.Run(func() {
		cfg.core.SetMetadataTags(span, maps.All(header))
		cfg.core.SetRequestTags(span, request.Any(), protocol)
		response, err = next(ctx, request)
	})
	return response, err
}

func (cfg *config) traceStreamingHandler(ctx context.Context, spec connectrpc.Spec, conn connectrpc.StreamingHandlerConn, next connectrpc.StreamingHandlerFunc) (err error) {
	protocol := connectshared.ProtocolFor(conn.Peer().Protocol)
	if !cfg.core.StreamCalls() {
		ctx = cfg.core.MarkHandled(ctx, instrumentation.ComponentServer, spec.Procedure, nil)
		return next(ctx, cfg.messageConn(ctx, spec, conn, protocol))
	}
	span, ctx := cfg.core.StartServerCall(ctx, sharedSpec(spec), protocol, nil, tracer.HTTPHeadersCarrier(conn.RequestHeader()))
	var guard connectshared.Guard
	// connect's handlers serve every call over HTTP.
	defer cfg.core.FinishOnReturn(&guard, &span, &err, &connectshared.SpanFinish{Procedure: spec.Procedure, Protocol: protocol, Mode: connectshared.FinishMode{Wire: true, HTTP: true}}, nil)
	guard.Run(func() {
		if cfg.core.MetadataTags() {
			cfg.core.SetMetadataTags(span, maps.All(conn.RequestHeader()))
		}
		err = next(ctx, cfg.messageConn(ctx, spec, conn, protocol))
	})
	return err
}

// messageConn returns the connection that the handler gets: conn, wrapped if its messages are
// traced.
func (cfg *config) messageConn(ctx context.Context, spec connectrpc.Spec, conn connectrpc.StreamingHandlerConn, protocol *connectshared.Protocol) connectrpc.StreamingHandlerConn {
	if !cfg.core.StreamMessages() {
		return conn
	}
	return newStreamingHandlerConn(ctx, cfg, conn, spec, protocol)
}

type streamingHandlerConn struct {
	connectrpc.StreamingHandlerConn
	cfg      *config
	messages connectshared.ServerMessages
	// header is a copy of the request headers when there is no call span, taken before the handler
	// runs: message spans extract their parent from it, and the first one is tagged with its
	// metadata. Send and Receive don't call RequestHeader, as the handler may modify the headers
	// concurrently.
	header http.Header
}

func newStreamingHandlerConn(ctx context.Context, cfg *config, conn connectrpc.StreamingHandlerConn, spec connectrpc.Spec, protocol *connectshared.Protocol) *streamingHandlerConn {
	stream := &streamingHandlerConn{StreamingHandlerConn: conn, cfg: cfg}
	if !cfg.core.StreamCalls() {
		stream.header = conn.RequestHeader().Clone()
	}
	stream.messages.Init(ctx, cfg.core, sharedSpec(spec), protocol, tracer.HTTPHeadersCarrier(stream.header))
	return stream
}

func (c *streamingHandlerConn) Receive(message any) (err error) {
	var span *tracer.Span
	var guard connectshared.Guard
	defer c.cfg.core.FinishOnReturn(&guard, &span, &err, c.messages.SpanFinish(), nil)
	guard.Run(func() {
		span = c.startMessageSpan()
		if err = c.StreamingHandlerConn.Receive(message); err == nil {
			c.messages.TagRequest(span, message)
		}
	})
	return err
}

func (c *streamingHandlerConn) Send(message any) (err error) {
	// Send(nil) only flushes the response headers.
	if message == nil {
		return c.StreamingHandlerConn.Send(message)
	}
	var span *tracer.Span
	var guard connectshared.Guard
	defer c.cfg.core.FinishOnReturn(&guard, &span, &err, c.messages.SpanFinish(), nil)
	guard.Run(func() {
		span = c.startMessageSpan()
		err = c.StreamingHandlerConn.Send(message)
	})
	return err
}

// startMessageSpan starts a message span, and tags the first one with the metadata when there is
// no call span to carry it.
func (c *streamingHandlerConn) startMessageSpan() *tracer.Span {
	span, tagMetadata := c.messages.Start()
	if tagMetadata {
		c.cfg.core.SetMetadataTags(span, maps.All(c.header))
	}
	return span
}
