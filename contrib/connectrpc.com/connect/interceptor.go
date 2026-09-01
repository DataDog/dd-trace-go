// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"context"
	"errors"
	"io"
	"sync"

	connectrpc "connectrpc.com/connect"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type interceptor struct {
	cfg         *config
	traceClient bool
	traceServer bool
}

// NewInterceptor returns an interceptor that traces both clients and handlers.
func NewInterceptor(opts ...Option) connectrpc.Interceptor {
	return newInterceptor(true, true, opts...)
}

// NewClientInterceptor returns an interceptor that traces clients.
func NewClientInterceptor(opts ...Option) connectrpc.Interceptor {
	return newInterceptor(true, false, opts...)
}

// NewServerInterceptor returns an interceptor that traces handlers.
func NewServerInterceptor(opts ...Option) connectrpc.Interceptor {
	return newInterceptor(false, true, opts...)
}

func newInterceptor(traceClient, traceServer bool, opts ...Option) connectrpc.Interceptor {
	cfg := newConfig(opts...)
	instr.Logger().Debug("contrib/connectrpc.com/connect: configuring interceptor: %#v", cfg)
	return &interceptor{cfg: cfg, traceClient: traceClient, traceServer: traceServer}
}

func (i *interceptor) WrapUnary(next connectrpc.UnaryFunc) connectrpc.UnaryFunc {
	return func(ctx context.Context, request connectrpc.AnyRequest) (connectrpc.AnyResponse, error) {
		spec := request.Spec()
		if _, untraced := i.cfg.untracedMethods[spec.Procedure]; untraced {
			return next(ctx, request)
		}
		if spec.IsClient {
			if !i.traceClient {
				return next(ctx, request)
			}
			return i.wrapUnaryClient(ctx, request, next)
		}
		if !i.traceServer {
			return next(ctx, request)
		}
		return i.wrapUnaryServer(ctx, request, next)
	}
}

func (i *interceptor) wrapUnaryClient(ctx context.Context, request connectrpc.AnyRequest, next connectrpc.UnaryFunc) (response connectrpc.AnyResponse, err error) {
	span, ctx := i.cfg.startCallSpan(ctx, request.Spec(), instrumentation.ComponentClient)
	// Unlike wrapUnaryServer, the span already exists here, so the defer is registered first and
	// these are read into cached locals afterward: if any of request's methods panic, the defer
	// (using whatever value each local ended up with, possibly still "") still finishes the span,
	// and — since the defer itself only ever reads the cached locals, never calling request's
	// methods again — a persistently panicking method can't abort cleanup a second time either.
	var protocol, procedure, httpMethod string
	defer func() {
		if recovered := recover(); recovered != nil {
			finishUnaryOnPanic(span, panicError(recovered), procedure, protocol, httpMethod, i.cfg)
			panic(recovered)
		}
		if recovered := recoverFinish(
			func() { finishUnary(span, err, procedure, protocol, httpMethod, i.cfg) },
			func(panicErr error) { finishUnaryOnPanic(span, panicErr, procedure, protocol, httpMethod, i.cfg) },
		); recovered != nil {
			panic(recovered)
		}
	}()
	protocol = request.Peer().Protocol
	procedure = request.Spec().Procedure
	httpMethod = request.HTTPMethod()
	setProtocolTag(span, protocol)
	setPeerTags(span, request.Peer())
	injectSpan(ctx, request.Header())
	withHeaderTags(i.cfg, request.Header(), protocol, span)
	withRequestTags(i.cfg, request.Any(), protocol, span)
	return next(ctx, request)
}

func (i *interceptor) wrapUnaryServer(ctx context.Context, request connectrpc.AnyRequest, next connectrpc.UnaryFunc) (response connectrpc.AnyResponse, err error) {
	// Read everything the defer needs from request before it's registered: it's called from
	// within an already-executing recover, so if request's implementation ever panicked on a
	// second call to one of these methods, that new panic would replace the original one and
	// abort cleanup (the same class of bug as the wrapped Streaming*Conn defers elsewhere).
	protocol := request.Peer().Protocol
	procedure := request.Spec().Procedure
	httpMethod := request.HTTPMethod()
	span, ctx := i.cfg.startCallSpan(ctx, request.Spec(), instrumentation.ComponentServer, propagationOptions(request.Header())...)
	defer func() {
		if recovered := recover(); recovered != nil {
			finishUnaryOnPanic(span, panicError(recovered), procedure, protocol, httpMethod, i.cfg)
			panic(recovered)
		}
		if recovered := recoverFinish(
			func() { finishUnary(span, err, procedure, protocol, httpMethod, i.cfg) },
			func(panicErr error) { finishUnaryOnPanic(span, panicErr, procedure, protocol, httpMethod, i.cfg) },
		); recovered != nil {
			panic(recovered)
		}
	}()
	setProtocolTag(span, protocol)
	withHeaderTags(i.cfg, request.Header(), protocol, span)
	withRequestTags(i.cfg, request.Any(), protocol, span)
	return next(ctx, request)
}

func (i *interceptor) WrapStreamingClient(next connectrpc.StreamingClientFunc) connectrpc.StreamingClientFunc {
	return func(ctx context.Context, spec connectrpc.Spec) connectrpc.StreamingClientConn {
		if !i.traceClient {
			return next(ctx, spec)
		}
		if _, untraced := i.cfg.untracedMethods[spec.Procedure]; untraced {
			return next(ctx, spec)
		}

		var callSpan *tracer.Span
		if i.cfg.traceStreamCalls {
			callSpan, ctx = i.cfg.startCallSpan(ctx, spec, instrumentation.ComponentClient)
		}
		var peer connectrpc.Peer
		defer func() {
			if recovered := recover(); recovered != nil {
				if callSpan != nil {
					setProtocolTag(callSpan, peer.Protocol)
				}
				finishCallOnPanic(callSpan, panicError(recovered), spec.Procedure, peer.Protocol, i.cfg)
				panic(recovered)
			}
		}()
		conn := next(ctx, spec)
		peer = conn.Peer()
		if callSpan != nil {
			setProtocolTag(callSpan, peer.Protocol)
			setPeerTags(callSpan, peer)
		}
		injectSpan(ctx, conn.RequestHeader())
		if callSpan == nil && !i.cfg.traceStreamMessages {
			return conn
		}
		// spec and peer are cached on the returned streamingClientConn instead of being re-derived
		// via conn.Spec()/conn.Peer() later: both are meant to be stable for the connection's
		// lifetime, and re-invoking those methods from within a panic-recovery defer (as earlier
		// review rounds found) risks a second panic replacing the original one and aborting
		// cleanup, if the wrapped conn's implementation ever panics on a later call.
		return newStreamingClientConn(ctx, i.cfg, conn, callSpan, spec, peer)
	}
}

func (i *interceptor) WrapStreamingHandler(next connectrpc.StreamingHandlerFunc) connectrpc.StreamingHandlerFunc {
	return func(ctx context.Context, conn connectrpc.StreamingHandlerConn) (err error) {
		if !i.traceServer {
			return next(ctx, conn)
		}
		spec := conn.Spec()
		if _, untraced := i.cfg.untracedMethods[spec.Procedure]; untraced {
			return next(ctx, conn)
		}

		protocol := conn.Peer().Protocol
		var callSpan *tracer.Span
		if i.cfg.traceStreamCalls {
			callSpan, ctx = i.cfg.startCallSpan(ctx, spec, instrumentation.ComponentServer, propagationOptions(conn.RequestHeader())...)
			defer func() {
				if recovered := recover(); recovered != nil {
					finishCallOnPanic(callSpan, panicError(recovered), spec.Procedure, protocol, i.cfg)
					panic(recovered)
				}
				if recovered := recoverFinish(
					func() { finishCall(callSpan, err, spec.Procedure, protocol, i.cfg) },
					func(panicErr error) { finishCallOnPanic(callSpan, panicErr, spec.Procedure, protocol, i.cfg) },
				); recovered != nil {
					panic(recovered)
				}
			}()
			setProtocolTag(callSpan, protocol)
			withHeaderTags(i.cfg, conn.RequestHeader(), protocol, callSpan)
		}
		if !i.cfg.traceStreamMessages {
			return next(ctx, conn)
		}
		return next(ctx, &streamingHandlerConn{
			StreamingHandlerConn: conn,
			cfg:                  i.cfg,
			ctx:                  ctx,
			spec:                 spec,
			protocol:             protocol,
		})
	}
}

type streamingClientConn struct {
	connectrpc.StreamingClientConn
	cfg  *config
	ctx  context.Context
	span *tracer.Span
	// spec and peer are captured once at construction (from values already computed in
	// WrapStreamingClient's own panic-protected setup), instead of being re-derived via
	// conn.Spec()/conn.Peer() from Send/Receive/finish. See the comment where this is
	// constructed for why.
	spec connectrpc.Spec
	peer connectrpc.Peer

	mu                   sync.Mutex
	active               int
	finishPending        bool
	terminalErr          error
	terminalErrIsPanic   bool
	terminalErrFallbacks []error
	finishOnce           sync.Once
	// stopAttemptOnce guards the (at most once, ever) attempt to stop the context-cancellation
	// watcher from endOperation. Without it, active reaching 0 more than once for the same
	// stream (e.g. a message operation finishes the stream, then CloseResponse's own
	// beginOperation/endOperation pair transiently re-triggers active==0) would call
	// stopContextDone a second time; context.AfterFunc's stop reports "not stopped" both when the
	// callback has started and when it was already stopped by an earlier call, and this package
	// can't tell those two apart, so a second call could wait on ctxFinishDone forever even
	// though the watcher will now never run at all. See endOperation.
	stopAttemptOnce sync.Once
	headerTagsOnce  sync.Once
	stopContextDone func() bool
	// ctxFinishDone is closed when requestFinish (the context.AfterFunc callback) returns,
	// regardless of whether it actually finished the stream. stopContextDone's own return value
	// only says whether the callback was prevented from starting at all — if the context is
	// already done, it may report "not stopped" while the callback is still running or has
	// already finished, and endOperation needs to know when it's actually safe to read
	// terminalErr/terminalErrFallbacks as final. See stopContextCallback.
	ctxFinishDone chan struct{}
}

func newStreamingClientConn(ctx context.Context, cfg *config, conn connectrpc.StreamingClientConn, span *tracer.Span, spec connectrpc.Spec, peer connectrpc.Peer) *streamingClientConn {
	stream := &streamingClientConn{
		StreamingClientConn: conn,
		cfg:                 cfg,
		ctx:                 ctx,
		span:                span,
		spec:                spec,
		peer:                peer,
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
	c.beginOperation()
	var messageSpan *tracer.Span
	defer func() {
		if recovered := recover(); recovered != nil {
			panicErr := panicError(recovered)
			finishMessageOnPanic(messageSpan, panicErr, c.spec.Procedure, c.peer.Protocol, c.cfg)
			c.endOperation(panicErr, true, true)
			panic(recovered)
		}
		if recovered := recoverFinish(
			func() { finishMessage(messageSpan, err, c.spec.Procedure, c.peer.Protocol, c.cfg) },
			func(panicErr error) {
				finishMessageOnPanic(messageSpan, panicErr, c.spec.Procedure, c.peer.Protocol, c.cfg)
			},
		); recovered != nil {
			c.endOperation(panicError(recovered), true, true)
			panic(recovered)
		}
		c.endOperation(err, err != nil && !isExpectedStreamEOF(err), false)
	}()
	if c.cfg.traceStreamMessages {
		messageSpan = c.cfg.startMessageSpan(c.ctx, c.spec, c.peer.Protocol, instrumentation.ComponentClient)
		setPeerTags(messageSpan, c.peer)
		withRequestTags(c.cfg, message, c.peer.Protocol, messageSpan)
	}
	c.headerTagsOnce.Do(func() {
		target := c.span
		if target == nil {
			target = messageSpan
		}
		withHeaderTags(c.cfg, c.RequestHeader(), c.peer.Protocol, target)
	})
	return c.StreamingClientConn.Send(message)
}

func (c *streamingClientConn) Receive(message any) (err error) {
	c.beginOperation()
	var messageSpan *tracer.Span
	defer func() {
		if recovered := recover(); recovered != nil {
			panicErr := panicError(recovered)
			finishMessageOnPanic(messageSpan, panicErr, c.spec.Procedure, c.peer.Protocol, c.cfg)
			c.endOperation(panicErr, true, true)
			panic(recovered)
		}
		if recovered := recoverFinish(
			func() { finishMessage(messageSpan, err, c.spec.Procedure, c.peer.Protocol, c.cfg) },
			func(panicErr error) {
				finishMessageOnPanic(messageSpan, panicErr, c.spec.Procedure, c.peer.Protocol, c.cfg)
			},
		); recovered != nil {
			c.endOperation(panicError(recovered), true, true)
			panic(recovered)
		}
		c.endOperation(err, err != nil, false)
	}()
	if c.cfg.traceStreamMessages {
		messageSpan = c.cfg.startMessageSpan(c.ctx, c.spec, c.peer.Protocol, instrumentation.ComponentClient)
		setPeerTags(messageSpan, c.peer)
	}
	c.headerTagsOnce.Do(func() {
		target := c.span
		if target == nil {
			target = messageSpan
		}
		withHeaderTags(c.cfg, c.RequestHeader(), c.peer.Protocol, target)
	})
	return c.StreamingClientConn.Receive(message)
}

func (c *streamingClientConn) CloseRequest() (err error) {
	c.beginOperation()
	defer func() {
		if recovered := recover(); recovered != nil {
			panicErr := panicError(recovered)
			c.endOperation(panicErr, true, true)
			panic(recovered)
		}
		c.endOperation(err, err != nil, false)
	}()
	if c.span != nil {
		c.headerTagsOnce.Do(func() {
			withHeaderTags(c.cfg, c.RequestHeader(), c.peer.Protocol, c.span)
		})
	}
	return c.StreamingClientConn.CloseRequest()
}

func (c *streamingClientConn) CloseResponse() (err error) {
	c.beginOperation()
	defer func() {
		if recovered := recover(); recovered != nil {
			panicErr := panicError(recovered)
			c.endOperation(panicErr, true, true)
			panic(recovered)
		}
		c.endOperation(err, true, false)
	}()
	return c.StreamingClientConn.CloseResponse()
}

func (c *streamingClientConn) beginOperation() {
	c.mu.Lock()
	c.active++
	c.mu.Unlock()
}

func (c *streamingClientConn) endOperation(err error, terminal, isPanic bool) {
	c.mu.Lock()
	if terminal {
		c.finishPending = true
		if err != nil && (isPanic || !isExpectedStreamEOF(err)) {
			c.recordTerminalError(err, isPanic)
		}
	}
	c.active--
	shouldFinish := c.finishPending && c.active == 0
	c.mu.Unlock()
	if !shouldFinish {
		return
	}
	// stopAttemptOnce: active can reach 0 more than once for the same stream (e.g. a message
	// operation finishes it, then CloseResponse's own beginOperation/endOperation pair
	// transiently re-triggers active == 0); only the first such transition needs to coordinate
	// with the context watcher at all, and doing it more than once risks trying to stop a watcher
	// that's already been stopped — see the struct field doc.
	c.stopAttemptOnce.Do(func() {
		if !c.stopContextCallback() {
			// The context watcher raced us: it's either currently running requestFinish or has
			// already finished doing so. Either way, terminalErr/terminalErrFallbacks might still
			// change (requestFinish could be about to record a more meaningful error) or might
			// already have changed without requestFinish calling finish itself (it may have
			// observed active > 0 moments before this call decremented it) — either way, this
			// must not read them as final until the watcher has fully returned.
			<-c.ctxFinishDone
		}
		c.finishFromSnapshot()
	})
}

func (c *streamingClientConn) requestFinish(err error) {
	if c.ctxFinishDone != nil {
		// Signal completion before returning, regardless of whether this call itself finishes
		// the stream — endOperation may be waiting to find out. Never wait on this channel from
		// here: this goroutine is what closes it.
		defer close(c.ctxFinishDone)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			// This runs in its own goroutine, spawned by context.AfterFunc, with no caller to
			// propagate a panic to — finish already finishes the call span as an error before
			// re-raising anything it recovers (see finish's own recoverFinish use), so the only
			// thing left to do here is stop it from crashing the whole host process.
			instr.Logger().Error("contrib/connectrpc.com/connect: recovered panic finishing stream after context cancellation: %v", recovered)
		}
	}()
	c.mu.Lock()
	c.finishPending = true
	if err != nil && !errors.Is(err, io.EOF) {
		c.recordTerminalError(err, false)
	}
	shouldFinish := c.active == 0
	c.mu.Unlock()
	if shouldFinish {
		c.finishFromSnapshot()
	}
}

func (c *streamingClientConn) finishFromSnapshot() {
	c.mu.Lock()
	terminalErr, terminalErrIsPanic := c.terminalErr, c.terminalErrIsPanic
	fallbacks := c.terminalErrFallbacks
	c.mu.Unlock()
	c.finish(terminalErr, terminalErrIsPanic, fallbacks)
}

// recordTerminalError updates the stream's terminal-error bookkeeping with a newly observed
// candidate. Must be called with c.mu held. A panic always wins outright, discarding any
// fallbacks (they no longer matter once a panic must be reported); a stored panic is never
// displaced by a later non-panic. Otherwise this relies only on isSuppressedTerminalError's
// code-based rules (never cfg.errCheck, which must run at most once per distinct error — see its
// own doc): a code-suppressed stored error is always replaced. A code-non-suppressed ("real")
// stored error is never displaced by another real candidate — concurrent Send, Receive, and a
// context-cancellation observation can each finish with a different, equally "real" by-code
// error, and always picking one over the others by arrival order would let cfg.errCheck's
// rejection of whichever one that is permanently hide the others' genuine failure. Instead every
// shadowed candidate is appended to terminalErrFallbacks, and finish tries them in arrival order
// if cfg.errCheck ends up rejecting the primary.
func (c *streamingClientConn) recordTerminalError(candidate error, isPanic bool) {
	switch {
	case c.terminalErr == nil:
		c.terminalErr = candidate
		c.terminalErrIsPanic = isPanic
	case isPanic:
		c.terminalErr = candidate
		c.terminalErrIsPanic = true
		c.terminalErrFallbacks = nil
	case c.terminalErrIsPanic:
		// A stored panic is never displaced.
	case isSuppressedTerminalError(c.terminalErr, c.cfg):
		c.terminalErr = candidate
	case !isSuppressedTerminalError(candidate, c.cfg):
		c.terminalErrFallbacks = append(c.terminalErrFallbacks, candidate)
	}
}

// stopContextCallback attempts to prevent the context-cancellation watcher (if any) from ever
// calling requestFinish, and reports whether that's guaranteed: true if there is no watcher, or
// if it was stopped before it could run at all. False means the watcher's context.AfterFunc
// callback has already started (or already finished) concurrently — see its own return value's
// documentation — and the caller must not trust an already-taken terminalErr snapshot without
// waiting for it first.
func (c *streamingClientConn) stopContextCallback() bool {
	if c.stopContextDone == nil {
		return true
	}
	return c.stopContextDone()
}

func (c *streamingClientConn) finish(err error, isPanic bool, fallbacks []error) {
	c.finishOnce.Do(func() {
		if c.span == nil {
			// WithStreamCalls(false) leaves no call span; finish still runs (for the
			// terminal-error bookkeeping and, via the caller, the context-watcher cleanup), but
			// there's nothing to tag or classify an error for. finishClassified would also no-op
			// on a nil span, but skip the classification (and any cfg.errCheck call) entirely
			// rather than do it for a result nothing will ever use.
			return
		}
		if isPanic {
			finishCallOnPanic(c.span, err, c.spec.Procedure, c.peer.Protocol, c.cfg)
			return
		}
		// cfg.errCheck runs as part of classification below (possibly more than once, if a
		// fallback is tried). finishOnce means this is the only chance this span gets to finish
		// at all, so a panic here — most likely from that user-supplied callback — must still
		// finish it (as an error) rather than leaving it open forever; recoverFinish handles
		// that, and the panic is then re-raised to whichever caller (endOperation or
		// requestFinish) invoked finish, exactly as any other panic from this package would be.
		if recovered := recoverFinish(
			func() {
				candidate := normalizeError(err)
				out := finishOutcomeFor(candidate, c.spec.Procedure, false, false, c.cfg)
				for _, fb := range fallbacks {
					if out.recorded != nil {
						break
					}
					fb = normalizeError(fb)
					if fbOut := finishOutcomeFor(fb, c.spec.Procedure, false, false, c.cfg); fbOut.recorded != nil {
						candidate, out = fb, fbOut
					}
				}
				finishClassified(c.span, candidate, out, c.peer.Protocol, c.cfg)
			},
			func(panicErr error) {
				finishCallOnPanic(c.span, panicErr, c.spec.Procedure, c.peer.Protocol, c.cfg)
			},
		); recovered != nil {
			panic(recovered)
		}
	})
}

type streamingHandlerConn struct {
	connectrpc.StreamingHandlerConn
	cfg *config
	ctx context.Context
	// spec and protocol are captured once at construction (from values already computed in
	// WrapStreamingHandler's own panic-protected setup), instead of being re-derived via
	// conn.Spec()/conn.Peer() from Receive/Send — see streamingClientConn's spec/peer fields for
	// why.
	spec       connectrpc.Spec
	protocol   string
	headerOnce sync.Once
}

// messageParentOpts returns a fresh propagated parent when there is no call span. An explicit
// span in c.ctx still takes precedence, while an inferred Orchestrion GLS parent yields to the
// propagated parent. Re-extracting avoids sharing a backfilled SpanContext between messages.
func (c *streamingHandlerConn) messageParentOpts() []tracer.StartSpanOption {
	if c.cfg.traceStreamCalls {
		return nil
	}
	return propagationOptions(c.RequestHeader())
}

func (c *streamingHandlerConn) Receive(message any) (err error) {
	span := c.cfg.startMessageSpan(c.ctx, c.spec, c.protocol, instrumentation.ComponentServer, c.messageParentOpts()...)
	defer func() {
		if recovered := recover(); recovered != nil {
			finishMessageOnPanic(span, panicError(recovered), c.spec.Procedure, c.protocol, c.cfg)
			panic(recovered)
		}
		if recovered := recoverFinish(
			func() { finishMessage(span, err, c.spec.Procedure, c.protocol, c.cfg) },
			func(panicErr error) { finishMessageOnPanic(span, panicErr, c.spec.Procedure, c.protocol, c.cfg) },
		); recovered != nil {
			panic(recovered)
		}
	}()
	c.headerOnce.Do(func() {
		withHeaderTags(c.cfg, c.RequestHeader(), c.protocol, span)
	})
	err = c.StreamingHandlerConn.Receive(message)
	if err == nil {
		withRequestTags(c.cfg, message, c.protocol, span)
	}
	return err
}

func (c *streamingHandlerConn) Send(message any) (err error) {
	span := c.cfg.startMessageSpan(c.ctx, c.spec, c.protocol, instrumentation.ComponentServer, c.messageParentOpts()...)
	defer func() {
		if recovered := recover(); recovered != nil {
			finishMessageOnPanic(span, panicError(recovered), c.spec.Procedure, c.protocol, c.cfg)
			panic(recovered)
		}
		if recovered := recoverFinish(
			func() { finishMessage(span, err, c.spec.Procedure, c.protocol, c.cfg) },
			func(panicErr error) { finishMessageOnPanic(span, panicErr, c.spec.Procedure, c.protocol, c.cfg) },
		); recovered != nil {
			panic(recovered)
		}
	}()
	c.headerOnce.Do(func() {
		withHeaderTags(c.cfg, c.RequestHeader(), c.protocol, span)
	})
	return c.StreamingHandlerConn.Send(message)
}
