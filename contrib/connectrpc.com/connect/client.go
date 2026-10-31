// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"context"
	"errors"
	"io"
	"maps"
	"sync/atomic"

	connectrpc "connectrpc.com/connect"

	"github.com/DataDog/dd-trace-go/v2/contrib/connectrpc.com/connectshared"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

func (cfg *config) traceUnaryClient(ctx context.Context, spec connectrpc.Spec, request connectrpc.AnyRequest, next connectrpc.UnaryFunc) (response connectrpc.AnyResponse, err error) {
	peer := request.Peer()
	protocol := connectshared.ProtocolFor(peer.Protocol)
	span, ctx := cfg.core.StartCallSpan(ctx, instrumentation.ComponentClient, sharedSpec(spec), protocol, request)
	finish := connectshared.SpanFinish{Procedure: spec.Procedure, Protocol: protocol}
	var guard connectshared.Guard
	defer cfg.core.FinishOnReturn(&guard, &span, &err, &finish, nil)
	guard.Run(func() {
		connectshared.NewPeerTags(peer.Addr).Set(span)
		header := request.Header()
		cfg.core.Inject(ctx, tracer.HTTPHeadersCarrier(header))
		cfg.core.SetMetadataTags(span, maps.All(header))
		cfg.core.SetRequestTags(span, request.Any(), protocol)
		response, err = next(ctx, request)
		// The client's HTTP method is only known once the request has been sent.
		finish.Mode = connectshared.UnaryFinishMode(protocol, request.HTTPMethod(), false)
	})
	return response, err
}

func (cfg *config) traceStreamingClient(ctx context.Context, spec connectrpc.Spec, next connectrpc.StreamingClientFunc) (conn connectrpc.StreamingClientConn) {
	var span *tracer.Span
	if cfg.core.StreamCalls() {
		span, ctx = cfg.core.StartCallSpan(ctx, instrumentation.ComponentClient, sharedSpec(spec), nil, nil)
	} else {
		ctx = cfg.core.MarkHandled(ctx, instrumentation.ComponentClient, spec.Procedure, nil)
	}
	var peer connectrpc.Peer
	var guard connectshared.Guard
	defer cfg.core.FinishOnOpenPanic(&guard, &span, func() *connectshared.Protocol {
		// Unknown if opening the connection panicked before it reported its protocol.
		return connectshared.ProtocolFor(peer.Protocol)
	}, spec.Procedure)
	guard.Run(func() {
		conn = next(ctx, spec)
		// The protocol is only known once the connection exists.
		peer = conn.Peer()
		protocol, peerTags := connectshared.ProtocolFor(peer.Protocol), connectshared.NewPeerTags(peer.Addr)
		if span != nil {
			connectshared.SetProtocolTags(span, protocol, spec.Procedure)
			peerTags.Set(span)
		}
		cfg.core.Inject(ctx, tracer.HTTPHeadersCarrier(conn.RequestHeader()))
		if span != nil || cfg.core.StreamMessages() {
			conn = newStreamingClientConn(ctx, cfg, conn, span, spec, protocol, peerTags)
		}
	})
	return conn
}

// streamingClientConn traces the operations of a client stream. Its call finishes the call span
// once the stream has reached a terminal state (or its context is done) and no operation is in
// flight.
type streamingClientConn struct {
	connectrpc.StreamingClientConn
	cfg *config
	// spec is cached so that no conn method is called while recovering from a panic.
	spec          connectrpc.Spec
	protocol      *connectshared.Protocol
	messages      connectshared.ClientMessages
	messageFinish connectshared.SpanFinish
	receives      atomic.Int32
	call          connectshared.ClientCall
}

func newStreamingClientConn(ctx context.Context, cfg *config, conn connectrpc.StreamingClientConn, span *tracer.Span, spec connectrpc.Spec, protocol *connectshared.Protocol, peer connectshared.PeerTags) *streamingClientConn {
	stream := &streamingClientConn{
		StreamingClientConn: conn,
		cfg:                 cfg,
		spec:                spec,
		protocol:            protocol,
		messageFinish:       connectshared.SpanFinish{Procedure: spec.Procedure, Protocol: protocol, Mode: messageFinishMode},
	}
	if cfg.core.StreamMessages() {
		stream.messages.Init(ctx, cfg.core, sharedSpec(spec), protocol, peer)
	}
	stream.call.Init(ctx, cfg.core, span, connectshared.SpanFinish{Procedure: spec.Procedure, Protocol: protocol})
	return stream
}

func (c *streamingClientConn) Send(message any) (err error) {
	traced := c.call.Begin()
	var span *tracer.Span
	var guard connectshared.Guard
	defer c.cfg.core.FinishOnReturn(&guard, &span, &err, &c.messageFinish, c.endSend)
	guard.Run(func() {
		// Send(nil) only flushes the request headers.
		if message != nil && traced && c.cfg.core.StreamMessages() {
			span = c.messages.Start()
			c.cfg.core.SetRequestTags(span, message, c.protocol)
		}
		c.tagMetadata(span, traced)
		err = c.StreamingClientConn.Send(message)
	})
	return err
}

func (c *streamingClientConn) Receive(message any) (err error) {
	traced := c.call.Begin()
	var span *tracer.Span
	var guard connectshared.Guard
	defer c.cfg.core.FinishOnReturn(&guard, &span, &err, &c.messageFinish, c.endReceive)
	guard.Run(func() {
		if traced && c.cfg.core.StreamMessages() {
			span = c.messages.Start()
		}
		err = c.StreamingClientConn.Receive(message)
		// The metadata is read before connect sends the request, so before any response is received.
		c.call.TagPendingMetadata(span)
	})
	return err
}

func (c *streamingClientConn) CloseRequest() (err error) {
	traced := c.call.Begin()
	var noSpan *tracer.Span
	var guard connectshared.Guard
	defer c.cfg.core.FinishOnReturn(&guard, &noSpan, &err, &c.messageFinish, c.endOnError)
	guard.Run(func() {
		c.tagMetadata(nil, traced)
		err = c.StreamingClientConn.CloseRequest()
	})
	return err
}

func (c *streamingClientConn) CloseResponse() (err error) {
	c.call.Begin()
	var noSpan *tracer.Span
	var guard connectshared.Guard
	defer c.cfg.core.FinishOnReturn(&guard, &noSpan, &err, &c.messageFinish, c.endAlways)
	guard.Run(func() { err = c.StreamingClientConn.CloseResponse() })
	return err
}

// endSend ends a Send. An error wrapping io.EOF means the server ended the stream, and Receive
// reports why.
func (c *streamingClientConn) endSend(err error, isPanic bool) {
	c.call.End(err, isPanic, err != nil && !c.cfg.core.IsExpectedStreamEOF(err), false)
}

// endReceive ends a Receive, whose message span records what it returned. The call span of a
// client-streaming RPC records what CloseAndReceive returns. CloseAndReceive reads the single
// response with two Receives: a first one that returns an error wrapping io.EOF, whatever its code,
// means that the response has no message; a second one that returns a message, that it has more
// than one; and a second one that returns an error wrapping io.EOF, that the RPC succeeded.
//
// A caller that reads the stream through ClientStreamForClient.Conn() makes the same calls, so it
// can't be told apart while it stops where CloseAndReceive does: after a first Receive that returns
// the end of the stream, after a second one that returns a message, or after a second one that
// returns a coded end of the stream. Its call span then records CloseAndReceive's result too:
// generated code calls CloseAndReceive; Conn is meant for wrapping the stream in another high-level
// API, which checks the response the same way; gRPC defines these cardinality violations as
// CodeUnimplemented; and connect.v2's transports check them for every caller. Only such a caller
// makes a third Receive, which undoes the verdict of a second one that returned a message: the
// stream then ends as any other.
func (c *streamingClientConn) endReceive(err error, isPanic bool) {
	if isPanic || c.spec.StreamType != connectrpc.StreamTypeClient {
		c.call.EndReceive(err, isPanic, false)
		return
	}
	switch c.receives.Add(1) {
	case 1:
		if errors.Is(err, io.EOF) {
			// The end of the response, which has no message, is CloseAndReceive's result: the end
			// of the context while this Receive ran is not.
			c.call.SetResult(connectrpc.NewError(connectrpc.CodeUnimplemented, errors.New("unary response has zero messages")))
			c.call.EndReceive(nil, false, true)
			return
		}
	case 2:
		switch {
		case err == nil:
			c.call.SetResult(connectrpc.NewError(connectrpc.CodeUnimplemented, errors.New("unary response has multiple messages")))
		case errors.Is(err, io.EOF):
			// The response is complete, which ends the RPC cleanly.
			c.call.EndReceive(nil, false, true)
			return
		}
	default:
		c.call.SetResult(nil)
	}
	c.call.EndReceive(err, false, false)
}

func (c *streamingClientConn) endOnError(err error, isPanic bool) {
	c.call.End(err, isPanic, err != nil, false)
}

func (c *streamingClientConn) endAlways(err error, isPanic bool) {
	c.call.End(err, isPanic, true, false)
}

// tagMetadata tags the request metadata once per stream (see connectshared.ClientCall.ClaimMetadata).
// Once connect has sent it, net/http may modify it while sending.
func (c *streamingClientConn) tagMetadata(messageSpan *tracer.Span, traced bool) {
	claimed, span, keep := c.call.ClaimMetadata(messageSpan, traced)
	if !claimed {
		return
	}
	header := c.RequestHeader()
	switch {
	case span != nil:
		c.cfg.core.SetMetadataTags(span, maps.All(header))
	case keep:
		c.call.KeepMetadata(header.Clone())
	}
}
