// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"context"
	"net/http"
	"sync/atomic"

	connectrpc "connectrpc.com/connect/v2"

	"github.com/DataDog/dd-trace-go/v2/contrib/connectrpc.com/connectshared"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

func (cfg *config) traceClient(ctx context.Context, spec connectrpc.Spec, next connectrpc.ClientFunc) (connectrpc.ClientStream, error) {
	info, ok := connectrpc.CallInfoForClientContext(ctx)
	if !ok {
		// Only outside of a connect.Client call: attach the CallInfo that the transport fills in
		// and that the call span is injected into.
		ctx, info = connectrpc.NewClientContext(ctx)
	}
	ctx, traced := cfg.core.ClaimCall(ctx, instrumentation.ComponentClient, spec.Procedure, info)
	if !traced {
		// A call to the same procedure that shares the CallInfo, such as one opened through another
		// client with the traced call's context, is passed through too: keep it in the trace. A call
		// to an untraced procedure sends no trace headers.
		passedThrough := cfg.core.PassedThroughTraced(ctx, instrumentation.ComponentClient, spec.Procedure, info)
		if !passedThrough {
			clearInjected(info.RequestHeader())
		}
		stream, err := next(ctx, spec)
		if err == nil && passedThrough {
			cfg.inject(ctx, info.RequestHeader())
		}
		return stream, err
	}
	return cfg.traceClientCall(ctx, spec, info, next)
}

func (cfg *config) traceClientCall(ctx context.Context, spec connectrpc.Spec, info *connectrpc.CallInfo, next connectrpc.ClientFunc) (stream connectrpc.ClientStream, err error) {
	unary := spec.StreamType == connectrpc.StreamTypeUnary
	var span *tracer.Span
	if unary || cfg.core.StreamCalls() {
		// The protocol is only known once the transport has opened the stream, and the span must
		// start before: the in-process transport serves the call on the context passed to next.
		span, ctx = cfg.core.StartCallSpan(ctx, instrumentation.ComponentClient, sharedSpec(spec), nil, info)
	} else {
		ctx = cfg.core.MarkHandled(ctx, instrumentation.ComponentClient, spec.Procedure, info)
	}
	// The interceptors and transport that next wraps may send or copy the request header: they
	// mustn't see the trace headers of an earlier call.
	clearInjected(info.RequestHeader())
	var protocol *connectshared.Protocol
	var guard connectshared.Guard
	defer cfg.core.FinishOnOpenPanic(&guard, &span, func() *connectshared.Protocol {
		if protocol == nil {
			protocol = openedProtocol(span, spec, info)
		}
		return protocol
	}, spec.Procedure)
	guard.Run(func() {
		stream, err = next(ctx, spec)
		protocol = openedProtocol(span, spec, info)
		if err != nil {
			// No stream, so no Close: connect.Client returns the error as is.
			stream = nil
			return
		}
		peer := connectshared.NewPeerTags(info.PeerAddr)
		peer.Set(span)
		header := info.RequestHeader()
		// The transport sends the request headers at the first SendHeaders, Send or CloseSend.
		// Injecting after next replaces the trace headers that a call made within next, sharing the
		// CallInfo, may have written.
		cfg.inject(ctx, header)
		if span != nil || cfg.core.StreamMessages() {
			stream = newClientStream(ctx, cfg, stream, span, spec, header, protocol, peer)
		}
	})
	if err != nil {
		// After guard.Run: a panic of the tracer while it records err leaves the span locked, so it
		// must not reach FinishOnOpenPanic, which would finish the span again.
		cfg.core.FinishSpan(span, err, connectshared.SpanFinish{Procedure: spec.Procedure, Protocol: protocol})
	}
	return stream, err
}

// openedProtocol returns the protocol of the call that next opened a stream for, or failed or
// panicked opening one, and sets its tags on span. It is read once, by the goroutine that opens the
// stream: a later call that reuses the CallInfo resets it. A call that failed to open has a
// protocol only if a transport carried it: an interceptor within this one may fail it before the
// transport runs, or after it opened the stream.
func openedProtocol(span *tracer.Span, spec connectrpc.Spec, info *connectrpc.CallInfo) *connectshared.Protocol {
	protocol := transportProtocol(info)
	connectshared.SetProtocolTags(span, protocol, spec.Procedure)
	return protocol
}

// clientStream traces the operations of a client stream. Its call finishes the call span once the
// RPC has reached a terminal state (or its context is done) and no operation is in flight.
type clientStream struct {
	// stream is not embedded, so that no method skips the bookkeeping.
	stream connectrpc.ClientStream
	cfg    *config
	// span is nil with WithStreamCalls(false).
	span *tracer.Span
	spec connectrpc.Spec
	// header is the CallInfo's request header. No other CallInfo field is read once the stream is
	// open: a later call that reuses the CallInfo resets them.
	header   *connectrpc.Header
	protocol *connectshared.Protocol
	// traceMessages is false for unary RPCs, which have no message spans.
	traceMessages bool
	// singleRequest and singleResponse are set when the caller sends, or receives, a single
	// message, as connect.Client does for unary and server-streaming, or unary and
	// client-streaming RPCs.
	singleRequest, singleResponse bool
	messages                      connectshared.ClientMessages
	messageFinish                 connectshared.SpanFinish
	// singleFinish classifies the message spans of a single-request stream's Send and of a
	// single-response stream's first Receive: they carry the call's only request or response, so
	// an io.EOF is an error.
	singleFinish connectshared.SpanFinish
	// receiveStarted is set by the first Receive of a single-response stream.
	receiveStarted atomic.Bool
	closing        atomic.Bool
	call           connectshared.ClientCall
}

func newClientStream(ctx context.Context, cfg *config, stream connectrpc.ClientStream, span *tracer.Span, spec connectrpc.Spec, header *connectrpc.Header, protocol *connectshared.Protocol, peer connectshared.PeerTags) *clientStream {
	unary := spec.StreamType == connectrpc.StreamTypeUnary
	s := &clientStream{
		stream:         stream,
		cfg:            cfg,
		span:           span,
		spec:           spec,
		header:         header,
		protocol:       protocol,
		traceMessages:  !unary && cfg.core.StreamMessages(),
		singleRequest:  unary || spec.StreamType == connectrpc.StreamTypeServer,
		singleResponse: unary || spec.StreamType == connectrpc.StreamTypeClient,
		messageFinish:  connectshared.SpanFinish{Procedure: spec.Procedure, Protocol: protocol, Mode: messageFinishMode},
		singleFinish:   connectshared.SpanFinish{Procedure: spec.Procedure, Protocol: protocol},
	}
	if s.traceMessages {
		s.messages.Init(ctx, cfg.core, sharedSpec(spec), protocol, peer)
	}
	callFinish := connectshared.SpanFinish{Procedure: spec.Procedure, Protocol: protocol}
	if unary {
		// connecthttp reports a not-modified error only for a Connect GET answered with a 304, so
		// the request's HTTP method is GET whenever one is reported.
		callFinish.Mode = connectshared.UnaryFinishMode(protocol, http.MethodGet, false)
	}
	s.call.Init(ctx, cfg.core, span, callFinish)
	return s
}

func (s *clientStream) SendHeaders() (err error) {
	traced := s.call.Begin()
	var noSpan *tracer.Span
	var guard connectshared.Guard
	defer s.cfg.core.FinishOnReturn(&guard, &noSpan, &err, &s.messageFinish, s.endWrite)
	guard.Run(func() {
		s.tagMetadata(nil, traced)
		err = s.stream.SendHeaders()
	})
	return err
}

func (s *clientStream) Send(message any) (err error) {
	traced := s.call.Begin()
	var span *tracer.Span
	finish := &s.messageFinish
	if s.singleRequest {
		finish = &s.singleFinish
	}
	var guard connectshared.Guard
	defer s.cfg.core.FinishOnReturn(&guard, &span, &err, finish, s.endWrite)
	guard.Run(func() {
		switch {
		case !traced:
		case s.spec.StreamType == connectrpc.StreamTypeUnary:
			s.cfg.core.SetRequestTags(s.span, message, s.protocol)
		case message != nil && s.traceMessages:
			// Send(nil) only flushes the request headers.
			span = s.messages.Start()
			s.cfg.core.SetRequestTags(span, message, s.protocol)
		}
		s.tagMetadata(span, traced)
		err = s.stream.Send(message)
	})
	return err
}

func (s *clientStream) CloseSend() (err error) {
	traced := s.call.Begin()
	var noSpan *tracer.Span
	var guard connectshared.Guard
	defer s.cfg.core.FinishOnReturn(&guard, &noSpan, &err, &s.messageFinish, s.endCloseSend)
	guard.Run(func() {
		s.tagMetadata(nil, traced)
		err = s.stream.CloseSend()
	})
	return err
}

func (s *clientStream) Receive(message any) (err error) {
	traced := s.call.Begin()
	var span *tracer.Span
	// outcome is the error that the spans see.
	var outcome error
	finish, end := &s.messageFinish, s.endReceive
	if s.singleResponse && !s.receiveStarted.Swap(true) {
		finish, end = &s.singleFinish, s.endResponse
	}
	var guard connectshared.Guard
	defer s.cfg.core.FinishOnReturn(&guard, &span, &outcome, finish, end)
	guard.Run(func() {
		if traced && s.traceMessages {
			span = s.messages.Start()
		}
		closedBefore := s.closing.Load()
		err = s.stream.Receive(message)
		// Close unblocks a pending Receive, which then fails because the stream was torn down: like
		// canceling the context, abandoning a stream with Close is not an error. A server's verdict
		// comes from the response, which Close only discards, so a Receive that was running before
		// Close keeps it. Other errors, such as connecthttp's for a rejecting HTTP status, are
		// dropped: telling them from the teardown would mean reading connecthttp's ClientInfo, which
		// races the transport unless the transport's own Receive has returned, and an interceptor
		// within this one may fail the Receive without calling it.
		if !s.closing.Load() || (!closedBefore && s.cfg.core.IsRemoteError(err)) {
			outcome = err
		}
		// The metadata is read before connect sends the request, so before any response is received.
		s.call.TagPendingMetadata(span)
	})
	return err
}

func (s *clientStream) Close() (err error) {
	s.call.Begin()
	var noSpan *tracer.Span
	var guard connectshared.Guard
	defer s.cfg.core.FinishOnReturn(&guard, &noSpan, &err, &s.messageFinish, s.endAlways)
	guard.Run(func() {
		s.closing.Store(true)
		err = s.stream.Close()
	})
	return err
}

// endWrite ends SendHeaders and Send. An error wrapping io.EOF means the server ended the stream,
// and Receive reports why. A single-request stream is different: its caller abandons it after any
// failed write, so every error ends it, and an EOF is an error.
func (s *clientStream) endWrite(err error, isPanic bool) {
	s.call.End(err, isPanic, err != nil && (s.singleRequest || !s.cfg.core.IsExpectedStreamEOF(err)), s.singleRequest)
}

func (s *clientStream) endCloseSend(err error, isPanic bool) {
	s.call.End(err, isPanic, err != nil, s.singleRequest)
}

func (s *clientStream) endReceive(err error, isPanic bool) {
	s.call.EndReceive(err, isPanic, false)
}

// endResponse ends the first Receive of a single-response stream, which reads its only response:
// its caller may never call Close (the generated CloseAndReceive doesn't), and an io.EOF means
// that the server sent none.
func (s *clientStream) endResponse(err error, isPanic bool) {
	s.call.EndReceive(err, isPanic, true)
}

func (s *clientStream) endAlways(err error, isPanic bool) {
	s.call.End(err, isPanic, true, false)
}

// tagMetadata tags the request metadata once per stream (see connectshared.ClientCall.ClaimMetadata).
// The write-side operations are SendHeaders, Send and CloseSend.
func (s *clientStream) tagMetadata(messageSpan *tracer.Span, traced bool) {
	switch _, span, keep := s.call.ClaimMetadata(messageSpan, traced); {
	case span != nil:
		s.cfg.core.SetMetadataTags(span, s.header.All())
	case keep:
		s.call.KeepMetadata(headerSnapshot(s.header))
	}
}
