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
	protocol := request.Peer().Protocol
	procedure := request.Spec().Procedure
	defer func() {
		if recovered := recover(); recovered != nil {
			finishUnaryOnPanic(span, panicError(recovered), procedure, protocol, request.HTTPMethod(), i.cfg)
			panic(recovered)
		}
		finishUnary(span, err, procedure, protocol, request.HTTPMethod(), i.cfg)
	}()
	setProtocolTag(span, protocol)
	setPeerTags(span, request.Peer())
	injectSpan(ctx, request.Header())
	withHeaderTags(i.cfg, request.Header(), protocol, span)
	withRequestTags(i.cfg, request.Any(), protocol, span)
	return next(ctx, request)
}

func (i *interceptor) wrapUnaryServer(ctx context.Context, request connectrpc.AnyRequest, next connectrpc.UnaryFunc) (response connectrpc.AnyResponse, err error) {
	protocol := request.Peer().Protocol
	procedure := request.Spec().Procedure
	span, ctx := i.cfg.startCallSpan(ctx, request.Spec(), instrumentation.ComponentServer, propagationOptions(request.Header())...)
	defer func() {
		if recovered := recover(); recovered != nil {
			finishUnaryOnPanic(span, panicError(recovered), procedure, protocol, request.HTTPMethod(), i.cfg)
			panic(recovered)
		}
		finishUnary(span, err, procedure, protocol, request.HTTPMethod(), i.cfg)
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
		protocol := ""
		defer func() {
			if recovered := recover(); recovered != nil {
				if callSpan != nil {
					setProtocolTag(callSpan, protocol)
				}
				finishCallOnPanic(callSpan, panicError(recovered), spec.Procedure, protocol, i.cfg)
				panic(recovered)
			}
		}()
		conn := next(ctx, spec)
		protocol = conn.Peer().Protocol
		if callSpan != nil {
			setProtocolTag(callSpan, protocol)
			setPeerTags(callSpan, conn.Peer())
		}
		injectSpan(ctx, conn.RequestHeader())
		if callSpan == nil && !i.cfg.traceStreamMessages {
			return conn
		}
		return newStreamingClientConn(ctx, i.cfg, conn, callSpan)
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
		parentOpts := propagationOptions(conn.RequestHeader())
		var callSpan *tracer.Span
		if i.cfg.traceStreamCalls {
			callSpan, ctx = i.cfg.startCallSpan(ctx, spec, instrumentation.ComponentServer, parentOpts...)
			setProtocolTag(callSpan, protocol)
			withHeaderTags(i.cfg, conn.RequestHeader(), protocol, callSpan)
			parentOpts = nil
			defer func() {
				if recovered := recover(); recovered != nil {
					finishCallOnPanic(callSpan, panicError(recovered), spec.Procedure, protocol, i.cfg)
					panic(recovered)
				}
				finishCall(callSpan, err, spec.Procedure, protocol, i.cfg)
			}()
		}
		if !i.cfg.traceStreamMessages {
			return next(ctx, conn)
		}
		return next(ctx, &streamingHandlerConn{
			StreamingHandlerConn: conn,
			cfg:                  i.cfg,
			ctx:                  ctx,
			parentOpts:           parentOpts,
		})
	}
}

type streamingClientConn struct {
	connectrpc.StreamingClientConn
	cfg  *config
	ctx  context.Context
	span *tracer.Span

	mu                 sync.Mutex
	active             int
	finishPending      bool
	terminalErr        error
	terminalErrFromCtx bool
	terminalErrIsPanic bool
	finishOnce         sync.Once
	headerTagsOnce     sync.Once
	stopContextDone    func() bool
}

func newStreamingClientConn(ctx context.Context, cfg *config, conn connectrpc.StreamingClientConn, span *tracer.Span) *streamingClientConn {
	stream := &streamingClientConn{
		StreamingClientConn: conn,
		cfg:                 cfg,
		ctx:                 ctx,
		span:                span,
	}
	if span != nil {
		stream.stopContextDone = context.AfterFunc(ctx, func() {
			stream.requestFinish(ctx.Err())
		})
	}
	return stream
}

func (c *streamingClientConn) Send(message any) (err error) {
	c.beginOperation()
	protocol := c.Peer().Protocol
	var messageSpan *tracer.Span
	if c.cfg.traceStreamMessages {
		messageSpan = c.cfg.startMessageSpan(c.ctx, c.Spec(), protocol, instrumentation.ComponentClient)
		setPeerTags(messageSpan, c.Peer())
		withRequestTags(c.cfg, message, protocol, messageSpan)
	}
	c.headerTagsOnce.Do(func() {
		target := c.span
		if target == nil {
			target = messageSpan
		}
		withHeaderTags(c.cfg, c.RequestHeader(), protocol, target)
	})
	defer func() {
		if recovered := recover(); recovered != nil {
			panicErr := panicError(recovered)
			finishMessageOnPanic(messageSpan, panicErr, c.Spec().Procedure, protocol, c.cfg)
			c.endOperation(panicErr, true, true)
			panic(recovered)
		}
		finishMessage(messageSpan, err, c.Spec().Procedure, protocol, c.cfg)
		c.endOperation(err, err != nil && !errors.Is(err, io.EOF), false)
	}()
	return c.StreamingClientConn.Send(message)
}

func (c *streamingClientConn) Receive(message any) (err error) {
	c.beginOperation()
	protocol := c.Peer().Protocol
	var messageSpan *tracer.Span
	if c.cfg.traceStreamMessages {
		messageSpan = c.cfg.startMessageSpan(c.ctx, c.Spec(), protocol, instrumentation.ComponentClient)
		setPeerTags(messageSpan, c.Peer())
	}
	c.headerTagsOnce.Do(func() {
		target := c.span
		if target == nil {
			target = messageSpan
		}
		withHeaderTags(c.cfg, c.RequestHeader(), protocol, target)
	})
	defer func() {
		if recovered := recover(); recovered != nil {
			panicErr := panicError(recovered)
			finishMessageOnPanic(messageSpan, panicErr, c.Spec().Procedure, protocol, c.cfg)
			c.endOperation(panicErr, true, true)
			panic(recovered)
		}
		finishMessage(messageSpan, err, c.Spec().Procedure, protocol, c.cfg)
		c.endOperation(err, err != nil, false)
	}()
	return c.StreamingClientConn.Receive(message)
}

func (c *streamingClientConn) CloseRequest() (err error) {
	c.beginOperation()
	protocol := c.Peer().Protocol
	if c.span != nil {
		c.headerTagsOnce.Do(func() {
			withHeaderTags(c.cfg, c.RequestHeader(), protocol, c.span)
		})
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			panicErr := panicError(recovered)
			c.endOperation(panicErr, true, true)
			panic(recovered)
		}
		c.endOperation(err, err != nil, false)
	}()
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
		if err != nil && (isPanic || !isExpectedStreamEOF(err)) && (c.terminalErr == nil || c.terminalErrFromCtx) {
			c.terminalErr = err
			c.terminalErrFromCtx = false
			c.terminalErrIsPanic = isPanic
		}
	}
	c.active--
	shouldFinish := c.finishPending && c.active == 0
	terminalErr := c.terminalErr
	terminalErrIsPanic := c.terminalErrIsPanic
	c.mu.Unlock()
	if shouldFinish {
		c.stopContextCallback()
		c.finish(terminalErr, terminalErrIsPanic)
	}
}

func (c *streamingClientConn) requestFinish(err error) {
	c.mu.Lock()
	c.finishPending = true
	if err != nil && !errors.Is(err, io.EOF) && c.terminalErr == nil {
		c.terminalErr = err
		c.terminalErrFromCtx = true
		c.terminalErrIsPanic = false
	}
	shouldFinish := c.active == 0
	terminalErr := c.terminalErr
	terminalErrIsPanic := c.terminalErrIsPanic
	c.mu.Unlock()
	if shouldFinish {
		c.finish(terminalErr, terminalErrIsPanic)
	}
}

func (c *streamingClientConn) stopContextCallback() {
	if c.stopContextDone != nil {
		c.stopContextDone()
	}
}

func (c *streamingClientConn) finish(err error, isPanic bool) {
	c.finishOnce.Do(func() {
		if isPanic {
			finishCallOnPanic(c.span, err, c.Spec().Procedure, c.Peer().Protocol, c.cfg)
			return
		}
		finishCall(c.span, err, c.Spec().Procedure, c.Peer().Protocol, c.cfg)
	})
}

type streamingHandlerConn struct {
	connectrpc.StreamingHandlerConn
	cfg        *config
	ctx        context.Context
	parentOpts []tracer.StartSpanOption
	headerOnce sync.Once
}

func (c *streamingHandlerConn) Receive(message any) (err error) {
	protocol := c.Peer().Protocol
	span := c.cfg.startMessageSpan(c.ctx, c.Spec(), protocol, instrumentation.ComponentServer, c.parentOpts...)
	c.headerOnce.Do(func() {
		withHeaderTags(c.cfg, c.RequestHeader(), protocol, span)
	})
	defer func() {
		if recovered := recover(); recovered != nil {
			finishMessageOnPanic(span, panicError(recovered), c.Spec().Procedure, protocol, c.cfg)
			panic(recovered)
		}
		finishMessage(span, err, c.Spec().Procedure, protocol, c.cfg)
	}()
	err = c.StreamingHandlerConn.Receive(message)
	if err == nil {
		withRequestTags(c.cfg, message, protocol, span)
	}
	return err
}

func (c *streamingHandlerConn) Send(message any) (err error) {
	protocol := c.Peer().Protocol
	span := c.cfg.startMessageSpan(c.ctx, c.Spec(), protocol, instrumentation.ComponentServer, c.parentOpts...)
	c.headerOnce.Do(func() {
		withHeaderTags(c.cfg, c.RequestHeader(), protocol, span)
	})
	defer func() {
		if recovered := recover(); recovered != nil {
			finishMessageOnPanic(span, panicError(recovered), c.Spec().Procedure, protocol, c.cfg)
			panic(recovered)
		}
		finishMessage(span, err, c.Spec().Procedure, protocol, c.cfg)
	}()
	return c.StreamingHandlerConn.Send(message)
}
