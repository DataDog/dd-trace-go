// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"context"
	"net/http"
	"sync/atomic"

	connectrpc "connectrpc.com/connect"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

func (cfg *config) traceUnaryHandler(ctx context.Context, spec connectrpc.Spec, request connectrpc.AnyRequest, next connectrpc.UnaryFunc) (response connectrpc.AnyResponse, err error) {
	protocol := protocolTagsFor(request.Peer().Protocol)
	httpMethod := request.HTTPMethod()
	header := request.Header()
	tags := make(map[string]any, 6)
	addProcedureTags(tags, spec)
	addProtocolTags(tags, protocol, spec.Procedure)
	span, ctx := cfg.startCallSpan(ctx, instrumentation.ComponentServer, spec.Procedure, request, tags, propagationOptions(header)...)
	finish := spanFinish{procedure: spec.Procedure, protocol: protocol, mode: unaryFinishMode(protocol, httpMethod)}
	defer cfg.finishOnReturn(&span, &err, &finish, nil)
	setMetadataTags(cfg, header, span)
	setRequestTags(cfg, request.Any(), protocol, span)
	return next(ctx, request)
}

func (cfg *config) traceStreamingHandler(ctx context.Context, spec connectrpc.Spec, conn connectrpc.StreamingHandlerConn, next connectrpc.StreamingHandlerFunc) (err error) {
	protocol := protocolTagsFor(conn.Peer().Protocol)
	if cfg.traceStreamCalls {
		tags := make(map[string]any, 6)
		addProcedureTags(tags, spec)
		addProtocolTags(tags, protocol, spec.Procedure)
		var span *tracer.Span
		span, ctx = cfg.startCallSpan(ctx, instrumentation.ComponentServer, spec.Procedure, nil, tags, propagationOptions(conn.RequestHeader())...)
		defer cfg.finishOnReturn(&span, &err, &spanFinish{procedure: spec.Procedure, protocol: protocol}, nil)
		if cfg.withMetadataTags {
			setMetadataTags(cfg, conn.RequestHeader(), span)
		}
	} else {
		ctx = markHandled(ctx, serverCallKey{}, callMarker{procedure: spec.Procedure, traced: true})
	}
	if !cfg.traceStreamMessages {
		return next(ctx, conn)
	}
	return next(ctx, newStreamingHandlerConn(ctx, cfg, conn, spec, protocol))
}

type streamingHandlerConn struct {
	connectrpc.StreamingHandlerConn
	cfg *config
	ctx context.Context
	// spec is cached so that no conn method is called while recovering from a panic.
	spec          connectrpc.Spec
	protocol      *protocolTags
	messageTags   tracer.StartSpanOption
	messageFinish spanFinish
	// header is a copy of the request headers when there is no call span, taken before the handler
	// runs: message spans extract their parent from it, and the first one is tagged with its
	// metadata. Send and Receive don't call RequestHeader, as the handler may modify the headers
	// concurrently.
	header         http.Header
	metadataTagged atomic.Bool
}

func newStreamingHandlerConn(ctx context.Context, cfg *config, conn connectrpc.StreamingHandlerConn, spec connectrpc.Spec, protocol *protocolTags) *streamingHandlerConn {
	stream := &streamingHandlerConn{
		StreamingHandlerConn: conn,
		cfg:                  cfg,
		ctx:                  ctx,
		spec:                 spec,
		protocol:             protocol,
		messageTags:          messageTags(spec, protocol),
		messageFinish:        spanFinish{procedure: spec.Procedure, protocol: protocol, mode: messageFinishMode},
	}
	if !cfg.traceStreamCalls {
		stream.header = conn.RequestHeader().Clone()
	}
	return stream
}

func (c *streamingHandlerConn) Receive(message any) (err error) {
	var span *tracer.Span
	defer c.cfg.finishOnReturn(&span, &err, &c.messageFinish, nil)
	span = c.cfg.startMessageSpan(c.ctx, c.messageTags, instrumentation.ComponentServer, c.messageParentOpts()...)
	c.tagMetadata(span)
	if err = c.StreamingHandlerConn.Receive(message); err == nil {
		setRequestTags(c.cfg, message, c.protocol, span)
	}
	return err
}

func (c *streamingHandlerConn) Send(message any) (err error) {
	// Send(nil) only flushes the response headers.
	if message == nil {
		return c.StreamingHandlerConn.Send(message)
	}
	var span *tracer.Span
	defer c.cfg.finishOnReturn(&span, &err, &c.messageFinish, nil)
	span = c.cfg.startMessageSpan(c.ctx, c.messageTags, instrumentation.ComponentServer, c.messageParentOpts()...)
	c.tagMetadata(span)
	return c.StreamingHandlerConn.Send(message)
}

// messageParentOpts extracts the propagated parent afresh for every message when there is no call
// span. An explicit span in c.ctx still wins, but an Orchestrion GLS fallback does not.
func (c *streamingHandlerConn) messageParentOpts() []tracer.StartSpanOption {
	if c.cfg.traceStreamCalls {
		return nil
	}
	return propagationOptions(c.header)
}

// tagMetadata tags the first message span when there is no call span to carry the metadata.
func (c *streamingHandlerConn) tagMetadata(span *tracer.Span) {
	if c.cfg.withMetadataTags && !c.cfg.traceStreamCalls && c.metadataTagged.CompareAndSwap(false, true) {
		setMetadataTags(c.cfg, c.header, span)
	}
}
