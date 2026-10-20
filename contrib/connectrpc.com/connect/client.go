// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"

	connectrpc "connectrpc.com/connect"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

func (cfg *config) traceUnaryClient(ctx context.Context, spec connectrpc.Spec, request connectrpc.AnyRequest, next connectrpc.UnaryFunc) (response connectrpc.AnyResponse, err error) {
	peer := request.Peer()
	protocol := protocolTagsFor(peer.Protocol)
	tags := make(map[string]any, 6)
	addProcedureTags(tags, spec)
	addProtocolTags(tags, protocol, spec.Procedure)
	span, ctx := cfg.startCallSpan(ctx, instrumentation.ComponentClient, spec.Procedure, request, tags)
	finish := spanFinish{procedure: spec.Procedure, protocol: protocol}
	defer cfg.finishOnReturn(&span, &err, &finish, nil)
	newPeerTags(peer).set(span)
	header := request.Header()
	injectSpan(ctx, header)
	setMetadataTags(cfg, header, span)
	setRequestTags(cfg, request.Any(), protocol, span)
	response, err = next(ctx, request)
	// The client's HTTP method is only known once the request has been sent.
	finish.mode = unaryFinishMode(protocol, request.HTTPMethod())
	return response, err
}

func (cfg *config) traceStreamingClient(ctx context.Context, spec connectrpc.Spec, next connectrpc.StreamingClientFunc) connectrpc.StreamingClientConn {
	var span *tracer.Span
	if cfg.traceStreamCalls {
		tags := make(map[string]any, 4)
		addProcedureTags(tags, spec)
		span, ctx = cfg.startCallSpan(ctx, instrumentation.ComponentClient, spec.Procedure, nil, tags)
	} else {
		ctx = markHandled(ctx, clientCallKey{}, callMarker{procedure: spec.Procedure, traced: true})
	}
	var peer connectrpc.Peer
	defer func() {
		if recovered := recover(); recovered != nil {
			protocol := protocolTagsFor(peer.Protocol)
			setProtocolTags(span, protocol, spec.Procedure)
			finishSpan(span, panicError(recovered), spec.Procedure, protocol, finishMode{isPanic: true}, cfg)
			panic(recovered)
		}
	}()
	conn := next(ctx, spec)
	// The protocol is only known once the connection exists.
	peer = conn.Peer()
	protocol, peerTags := protocolTagsFor(peer.Protocol), newPeerTags(peer)
	if span != nil {
		setProtocolTags(span, protocol, spec.Procedure)
		peerTags.set(span)
	}
	injectSpan(ctx, conn.RequestHeader())
	if span == nil && !cfg.traceStreamMessages {
		return conn
	}
	return newStreamingClientConn(ctx, cfg, conn, span, spec, protocol, peerTags)
}

// streamingClientConn finishes the call span once the stream has reached a terminal state (or its
// context is done) and no operation is in flight.
type streamingClientConn struct {
	connectrpc.StreamingClientConn
	cfg *config
	ctx context.Context
	// span is nil with WithStreamCalls(false); the conn then does no call-span bookkeeping.
	span *tracer.Span
	// spec is cached so that no conn method is called while recovering from a panic.
	spec          connectrpc.Spec
	protocol      *protocolTags
	peer          peerTags
	messageTags   tracer.StartSpanOption
	messageFinish spanFinish

	// metadataRead is set by the first write-side operation, the only one that reads the metadata.
	metadataRead atomic.Bool
	// pendingMetadata holds, without a call span, a copy of the metadata for the next message span
	// when the operation that read it had none.
	pendingMetadata atomic.Pointer[http.Header]
	stopContextDone func() bool
	// ctxFinishDone is closed when requestFinish returns.
	ctxFinishDone chan struct{}

	mu            sync.Mutex
	active        int
	finishPending bool
	// finishClaimed is set, under mu, by the one caller that finishes the call span.
	finishClaimed atomic.Bool
	terminal      terminalCandidate
	fallbacks     []error
}

// terminalCandidate is an error that may end the stream, classified outside of mu because
// classification runs user-defined error methods.
type terminalCandidate struct {
	err        error
	isPanic    bool
	suppressed bool
}

func newStreamingClientConn(ctx context.Context, cfg *config, conn connectrpc.StreamingClientConn, span *tracer.Span, spec connectrpc.Spec, protocol *protocolTags, peer peerTags) *streamingClientConn {
	stream := &streamingClientConn{
		StreamingClientConn: conn,
		cfg:                 cfg,
		ctx:                 ctx,
		span:                span,
		spec:                spec,
		protocol:            protocol,
		peer:                peer,
		messageFinish:       spanFinish{procedure: spec.Procedure, protocol: protocol, mode: messageFinishMode},
	}
	if cfg.traceStreamMessages {
		stream.messageTags = messageTags(spec, protocol)
	}
	if span != nil {
		stream.ctxFinishDone = make(chan struct{})
		stream.stopContextDone = context.AfterFunc(ctx, func() {
			stream.requestFinish(ctx.Err())
		})
	}
	return stream
}

func (c *streamingClientConn) Send(message any) (err error) {
	traced := c.beginOperation()
	var span *tracer.Span
	defer c.cfg.finishOnReturn(&span, &err, &c.messageFinish, c.endSend)
	// Send(nil) only flushes the request headers.
	if message != nil && traced && c.cfg.traceStreamMessages {
		span = c.startMessageSpan()
		setRequestTags(c.cfg, message, c.protocol, span)
	}
	c.tagMetadata(span, traced)
	return c.StreamingClientConn.Send(message)
}

func (c *streamingClientConn) Receive(message any) (err error) {
	traced := c.beginOperation()
	var span *tracer.Span
	defer c.cfg.finishOnReturn(&span, &err, &c.messageFinish, c.endOnError)
	if traced && c.cfg.traceStreamMessages {
		span = c.startMessageSpan()
	}
	err = c.StreamingClientConn.Receive(message)
	// The metadata is read before connect sends the request, so before any response is received.
	c.tagPendingMetadata(span)
	return err
}

func (c *streamingClientConn) CloseRequest() (err error) {
	traced := c.beginOperation()
	var noSpan *tracer.Span
	defer c.cfg.finishOnReturn(&noSpan, &err, &c.messageFinish, c.endOnError)
	c.tagMetadata(nil, traced)
	return c.StreamingClientConn.CloseRequest()
}

func (c *streamingClientConn) CloseResponse() (err error) {
	c.beginOperation()
	var noSpan *tracer.Span
	defer c.cfg.finishOnReturn(&noSpan, &err, &c.messageFinish, c.endAlways)
	return c.StreamingClientConn.CloseResponse()
}

func (c *streamingClientConn) startMessageSpan() *tracer.Span {
	span := c.cfg.startMessageSpan(c.ctx, c.messageTags, instrumentation.ComponentClient)
	c.peer.set(span)
	return span
}

// endSend ends a Send. An error wrapping io.EOF means the server ended the stream, and Receive
// reports why.
func (c *streamingClientConn) endSend(err error, isPanic bool) {
	c.endOperation(err, isPanic || (err != nil && !isExpectedStreamEOF(err)), isPanic)
}

func (c *streamingClientConn) endOnError(err error, isPanic bool) {
	c.endOperation(err, isPanic || err != nil, isPanic)
}

func (c *streamingClientConn) endAlways(err error, isPanic bool) {
	c.endOperation(err, true, isPanic)
}

// tagMetadata tags the request metadata once per stream: on the call span or, without one, on this
// operation's message span or else the next one (see tagPendingMetadata). Only the first
// write-side operation (Send or CloseRequest) reads the metadata, before connect sends it:
// afterwards net/http may modify it while sending, and Receive may run concurrently with the
// application setting it.
func (c *streamingClientConn) tagMetadata(messageSpan *tracer.Span, traced bool) {
	if !c.cfg.withMetadataTags {
		return
	}
	if !c.metadataRead.CompareAndSwap(false, true) {
		c.tagPendingMetadata(messageSpan)
		return
	}
	header := c.RequestHeader()
	switch {
	case c.span != nil:
		if traced {
			setMetadataTags(c.cfg, header, c.span)
		}
	case messageSpan != nil:
		setMetadataTags(c.cfg, header, messageSpan)
	default:
		pending := header.Clone()
		c.pendingMetadata.Store(&pending)
	}
}

// tagPendingMetadata tags span with the metadata that tagMetadata could not put on a message span.
func (c *streamingClientConn) tagPendingMetadata(span *tracer.Span) {
	if span == nil || !c.cfg.withMetadataTags {
		return
	}
	if header := c.pendingMetadata.Swap(nil); header != nil {
		setMetadataTags(c.cfg, *header, span)
	}
}

// beginOperation starts an operation and reports whether the stream is still traced. Once the call
// span's finish has been claimed, its trace may have been flushed and recycled by the span pool, so
// it must not be tagged nor parent message spans.
func (c *streamingClientConn) beginOperation() (traced bool) {
	if c.span == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active++
	return !c.finishClaimed.Load()
}

// endOperation ends an operation started by beginOperation. err must be normalized; terminal
// reports whether the operation ended the stream.
func (c *streamingClientConn) endOperation(err error, terminal, isPanic bool) {
	if c.span == nil {
		return
	}
	var candidate terminalCandidate
	var record bool
	if terminal && !c.finishClaimed.Load() {
		candidate, record = c.newTerminalCandidate(err, isPanic)
	}
	c.mu.Lock()
	if terminal {
		c.finishPending = true
		if record {
			c.recordTerminal(candidate)
		}
	}
	c.active--
	// active can reach 0 more than once, and errCheck may reenter the stream from finish: only the
	// caller that claims the finish stops the context watcher.
	claimed := c.finishPending && c.active == 0 && c.finishClaimed.CompareAndSwap(false, true)
	c.mu.Unlock()
	if !claimed {
		return
	}
	if !c.stopContextDone() {
		// The watcher is running or has run: wait until it is done with the terminal state.
		<-c.ctxFinishDone
	}
	c.finish()
}

// requestFinish is the context watcher. It runs in its own goroutine, so it must not panic.
func (c *streamingClientConn) requestFinish(err error) {
	defer close(c.ctxFinishDone)
	defer func() {
		if recovered := recover(); recovered != nil {
			instr.Logger().Error("contrib/connectrpc.com/connect: recovered panic finishing stream after context cancellation: %v", recovered)
		}
	}()
	candidate, record := c.newTerminalCandidate(normalizeError(err), false)
	c.mu.Lock()
	c.finishPending = true
	if record {
		c.recordTerminal(candidate)
	}
	claimed := c.active == 0 && c.finishClaimed.CompareAndSwap(false, true)
	c.mu.Unlock()
	if claimed {
		c.finish()
	}
}

// newTerminalCandidate reports whether err ends the stream with an error, and classifies it.
func (c *streamingClientConn) newTerminalCandidate(err error, isPanic bool) (terminalCandidate, bool) {
	if err == nil || (!isPanic && isExpectedStreamEOF(err)) {
		return terminalCandidate{}, false
	}
	return terminalCandidate{
		err:        err,
		isPanic:    isPanic,
		suppressed: !isPanic && isSuppressedTerminalError(err, c.cfg),
	}, true
}

// recordTerminal must be called with c.mu held. A panic wins and is never displaced. Otherwise a
// suppressed error is replaced, and later unsuppressed errors are kept, in order, as fallbacks in
// case errCheck rejects the first one.
func (c *streamingClientConn) recordTerminal(candidate terminalCandidate) {
	switch {
	case c.terminal.err == nil:
		c.terminal = candidate
	case candidate.isPanic:
		c.terminal = candidate
		c.fallbacks = nil
	case c.terminal.isPanic:
		// Keep the panic.
	case c.terminal.suppressed:
		c.terminal = candidate
	case !candidate.suppressed:
		c.fallbacks = append(c.fallbacks, candidate.err)
	}
}

// finish finishes the call span; the caller must have claimed it. errCheck runs here, outside of
// any lock, and may reenter the stream.
func (c *streamingClientConn) finish() {
	c.mu.Lock()
	terminal, fallbacks := c.terminal, c.fallbacks
	c.mu.Unlock()
	if terminal.isPanic {
		finishSpan(c.span, terminal.err, c.spec.Procedure, c.protocol, finishMode{isPanic: true}, c.cfg)
		return
	}
	if recovered := recoverFinish(
		func() {
			err, out := c.classifyTerminal(terminal.err, fallbacks)
			finishClassified(c.span, err, out, c.protocol, c.cfg)
		},
		func(panicErr error) {
			finishSpan(c.span, panicErr, c.spec.Procedure, c.protocol, finishMode{isPanic: true}, c.cfg)
		},
	); recovered != nil {
		panic(recovered)
	}
}

// classifyTerminal classifies err and, while errCheck rejects the chosen error, each fallback in
// arrival order.
func (c *streamingClientConn) classifyTerminal(err error, fallbacks []error) (error, finishOutcome) {
	err = normalizeError(err)
	out := finishOutcomeFor(err, c.spec.Procedure, finishMode{}, c.cfg)
	for _, fallback := range fallbacks {
		if out.recorded != nil {
			break
		}
		fallback = normalizeError(fallback)
		if fallbackOut := finishOutcomeFor(fallback, c.spec.Procedure, finishMode{}, c.cfg); fallbackOut.recorded != nil {
			err, out = fallback, fallbackOut
		}
	}
	return err, out
}
