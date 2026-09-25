// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttp

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/valyala/fasthttp"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
)

// TimeoutHandler returns a handler that sends a 408 response with msg when h
// exceeds timeout. It coordinates tracing and AppSec with the timeout worker,
// so it can be used inside or outside WrapHandler. Use this function instead of
// fasthttp.TimeoutHandler when the handler is instrumented.
//
// A timeout does not stop h. The request span and AppSec monitoring end at the
// timeout; the worker retains its RequestCtx until h returns. Request-body,
// response-body, and RASP checks made afterward do not run the WAF.
// Nested timeout handlers share one worker; the earliest active deadline wins.
// The first traced scope covers the whole worker, including subsequent traced calls.
// Resource namers run before h starts, while the worker is paused, and again
// after h returns. A timed-out request keeps the resource from the first call.
// The worker limit defaults to fasthttp.DefaultConcurrency and is separate from
// Server.Concurrency. Use WithTimeoutConcurrency to change it. Excess requests
// receive 429. A non-positive timeout disables this wrapper.
//
// Do not enable span pooling with [tracer.WithSpanPool] when using these wrappers.
// A timed-out worker can otherwise use a span recycled for another request.
// Span pooling is disabled by default.
func TimeoutHandler(h fasthttp.RequestHandler, timeout time.Duration, msg string, opts ...TimeoutOption) fasthttp.RequestHandler {
	return TimeoutWithCodeHandler(h, timeout, msg, fasthttp.StatusRequestTimeout, opts...)
}

// TimeoutWithCodeHandler is TimeoutHandler with a custom timeout status code.
func TimeoutWithCodeHandler(h fasthttp.RequestHandler, timeout time.Duration, msg string, statusCode int, opts ...TimeoutOption) fasthttp.RequestHandler {
	cfg := timeoutOptions{concurrency: fasthttp.DefaultConcurrency}
	for _, option := range opts {
		option(&cfg)
	}
	return timeoutWithCodeHandler(h, timeout, msg, statusCode, cfg.concurrency)
}

// TimeoutOption configures TimeoutHandler or TimeoutWithCodeHandler.
type TimeoutOption func(*timeoutOptions)

type timeoutOptions struct {
	concurrency int
}

// WithTimeoutConcurrency limits the outstanding workers of one timeout wrapper.
// Timed-out workers keep their slots until their handlers return. Reuse a wrapper
// to share its limit across requests. Nested wrappers use only the outer worker
// limit. This option panics if the limit is not positive.
func WithTimeoutConcurrency(limit int) TimeoutOption {
	if limit <= 0 {
		panic("fasthttp: timeout concurrency must be positive")
	}
	return func(cfg *timeoutOptions) { cfg.concurrency = limit }
}

func timeoutWithCodeHandler(h fasthttp.RequestHandler, timeout time.Duration, msg string, statusCode, concurrency int) fasthttp.RequestHandler {
	if timeout <= 0 {
		return h
	}
	workers := make(chan struct{}, concurrency)
	return func(ctx *fasthttp.RequestCtx) {
		deadline := timeoutDeadline{at: time.Now().Add(timeout), message: msg, status: statusCode}
		if layer, ok := ctx.UserValue(timeoutContextKey{}).(*timeoutLayer); ok {
			nested := &deadline
			if layer.exchange(timeoutEvent{deadline: nested, addDeadline: true}) {
				defer layer.exchange(timeoutEvent{deadline: nested})
			}
			h(ctx)
			return
		}
		select {
		case workers <- struct{}{}:
		default:
			ctx.Error(msg, fasthttp.StatusTooManyRequests)
			return
		}
		started := false
		defer func() {
			if !started {
				<-workers
			}
		}()
		layer := newTimeoutLayer(ctx, deadline)
		started = true
		layer.run(h, workers)
	}
}

type timeoutContextKey struct{}

type timeoutDeadline struct {
	at      time.Time
	message string
	status  int
}

type timeoutEvent struct {
	cfg         *config
	scope       *handlerScope
	deadline    *timeoutDeadline
	addDeadline bool
	// started receives the scope that the owner starts for a cfg event. The
	// owner writes it before it sends the reply.
	started **handlerScope
	reply   chan struct{}
}

type timeoutLayer struct {
	ctx    *fasthttp.RequestCtx
	events chan timeoutEvent
	// Closing stopped also releases the worker after it has closed workerDone.
	stopped      chan struct{}
	workerDone   chan struct{}
	workerExited chan struct{}

	// The owner alone changes these fields. Closing stopped publishes them to
	// the worker; it must not inspect them before that channel is closed.
	scopes             []*handlerScope
	outerCount         int
	rootWorker         *handlerScope
	deadlines          []*timeoutDeadline
	timedOut           bool
	aborted            bool
	workerPanicHandled bool
	blockedResponse    *fasthttp.Response
	response           fasthttp.Response
	stopOnce           sync.Once

	// Storage for the usual number of deadlines and scopes. These fields avoid
	// allocations for each request.
	firstDeadline timeoutDeadline
	deadlineBuf   [1]*timeoutDeadline
	scopeBuf      [2]*handlerScope

	// Written by the worker before closing workerDone.
	panicValue any
	panicked   bool
}

func newTimeoutLayer(ctx *fasthttp.RequestCtx, deadline timeoutDeadline) *timeoutLayer {
	layer := &timeoutLayer{
		ctx:           ctx,
		events:        make(chan timeoutEvent),
		stopped:       make(chan struct{}),
		workerDone:    make(chan struct{}),
		workerExited:  make(chan struct{}),
		firstDeadline: deadline,
	}
	layer.deadlineBuf[0] = &layer.firstDeadline
	layer.deadlines = layer.deadlineBuf[:]
	layer.scopes = layer.scopeBuf[:0]
	for scope, _ := ctx.UserValue(handlerScopeKey{}).(*handlerScope); scope != nil; scope = scope.parent {
		layer.scopes = append(layer.scopes, scope)
	}
	for i, j := 0, len(layer.scopes)-1; i < j; i, j = i+1, j-1 {
		layer.scopes[i], layer.scopes[j] = layer.scopes[j], layer.scopes[i]
	}
	// A timed-out request uses these resources. No worker runs yet, so the
	// resource namers can read the context safely.
	for _, scope := range layer.scopes {
		scope.setResource()
	}
	for _, scope := range layer.scopes {
		scope.layer = layer
	}
	layer.outerCount = len(layer.scopes)
	ctx.SetUserValue(timeoutContextKey{}, layer)
	return layer
}

func (l *timeoutLayer) stop() {
	l.stopOnce.Do(func() { close(l.stopped) })
}

// exchange sends event to the owner and waits for its reply. It returns false
// if the owner stopped before it replied.
func (l *timeoutLayer) exchange(event timeoutEvent) bool {
	event.reply = make(chan struct{}, 1)
	select {
	case l.events <- event:
	case <-l.stopped:
		return false
	}
	select {
	case <-event.reply:
		return true
	case <-l.stopped:
		// The owner may have queued a block decision before the timeout.
		// It sends replies before closing stopped; do not discard that reply.
		select {
		case <-event.reply:
			return true
		default:
			return false
		}
	}
}

func (l *timeoutLayer) wrapHandler(ctx *fasthttp.RequestCtx, h fasthttp.RequestHandler, cfg *config) {
	var scope *handlerScope
	if !l.exchange(timeoutEvent{cfg: cfg, started: &scope}) {
		if !l.aborted {
			h(ctx)
		}
		return
	}
	defer func() {
		if !l.exchange(timeoutEvent{scope: scope}) {
			// The owner has finished against a detached response. Only this
			// worker may now change the live context's user values.
			scope.restore()
		}
	}()
	defer activateTimeoutWorker(ctx, scope)()
	if !scope.handled {
		h(ctx)
	}
}

// A separate operation owns the worker's GLS binding. The HTTP operation starts
// and finishes on the timeout owner, while this binding starts and finishes on
// the worker. It adds no second registration of the HTTP operation itself.
type timeoutWorkerOperation struct{ dyngo.Operation }
type timeoutWorkerResult struct{}

func (timeoutWorkerResult) IsResultOf(*timeoutWorkerOperation) {}

func activateTimeoutWorker(ctx context.Context, scope *handlerScope) func() {
	if scope == nil {
		return func() {}
	}
	ctx = tracer.ContextWithSpan(ctx, scope.span)
	if scope.appsec == nil {
		return func() {}
	}
	op := &timeoutWorkerOperation{Operation: dyngo.NewOperation(scope.appsec.op)}
	dyngo.RegisterOperation(ctx, op)
	return func() { dyngo.FinishOperation(op, timeoutWorkerResult{}) }
}

func (l *timeoutLayer) run(h fasthttp.RequestHandler, workers chan struct{}) {
	var parent *handlerScope
	if len(l.scopes) != 0 {
		parent = l.scopes[len(l.scopes)-1]
	}
	go func() {
		returned := false
		defer func() {
			l.panicValue = recover()
			l.panicked = !returned
			close(l.workerDone)
			<-l.stopped
			if l.timedOut {
				for _, scope := range slices.Backward(l.scopes) {
					scope.restore()
				}
				l.ctx.RemoveUserValue(timeoutContextKey{})
			}
			<-workers
			close(l.workerExited)
			if l.panicked && l.timedOut && !l.workerPanicHandled {
				panic(l.panicValue)
			}
		}()
		defer activateTimeoutWorker(l.ctx, parent)()
		h(l.ctx)
		returned = true
	}()
	defer func() {
		// Stopping also releases the worker.
		l.stop()
		if !l.timedOut {
			// The handler has returned. Release its worker slot before a
			// caller can reuse this wrapper for another request.
			<-l.workerExited
		}
	}()
	completed := false
	defer func() {
		if !completed {
			l.abort()
		}
	}()

	timer := acquireTimer(time.Until(l.earliestDeadline().at))
	defer releaseTimer(timer)
	for {
		select {
		case event := <-l.events:
			switch {
			case event.cfg != nil:
				scope := startHandlerScope(l.ctx, event.cfg)
				l.scopes = append(l.scopes, scope)
				if l.rootWorker == nil {
					l.rootWorker = scope
				}
				scope.setResource()
				if scope.handled && l.blockedResponse == nil {
					// The worker is paused here. Preserve an early block before
					// application code can change the live response again.
					l.blockedResponse = new(fasthttp.Response)
					l.ctx.Response.CopyTo(l.blockedResponse)
				}
				*event.started = scope
				event.reply <- struct{}{}
			case event.scope != nil:
				if event.scope != l.rootWorker {
					// The worker waits for this reply, so the resource namer
					// can read what the handler stored in the context.
					event.scope.setResource()
					l.finishScope(event.scope, &l.ctx.Response, true)
					// Finished nested scopes must not accumulate in a long request.
					for i, scope := range l.scopes {
						if scope == event.scope {
							copy(l.scopes[i:], l.scopes[i+1:])
							l.scopes[len(l.scopes)-1] = nil
							l.scopes = l.scopes[:len(l.scopes)-1]
							break
						}
					}
				}
				event.reply <- struct{}{}
			case event.deadline != nil:
				if event.addDeadline {
					l.deadlines = append(l.deadlines, event.deadline)
				} else {
					for i, deadline := range l.deadlines {
						if deadline == event.deadline {
							l.deadlines = append(l.deadlines[:i], l.deadlines[i+1:]...)
							break
						}
					}
				}
				timer.Reset(time.Until(l.earliestDeadline().at))
				event.reply <- struct{}{}
			}
		case <-l.workerDone:
			l.workerPanicHandled = true
			// No application code can now access the live response or values.
			if l.rootWorker != nil {
				l.rootWorker.setResource()
				l.rootWorker.finishAppSec(&l.ctx.Response)
				l.rootWorker.restore()
			}
			l.ctx.RemoveUserValue(timeoutContextKey{})
			if l.rootWorker != nil {
				l.rootWorker.finishSpan(&l.ctx.Response)
			}
			// A later timeout call may adopt these same outer scopes. This
			// layer must leave no pending span or ownership behind.
			for _, scope := range l.scopes[:l.outerCount] {
				scope.layer = nil
			}
			completed = true
			if l.panicked {
				panic(l.panicValue)
			}
			return
		case <-timer.C:
			l.timedOut = true
			if l.blockedResponse != nil {
				l.blockedResponse.CopyTo(&l.response)
			} else {
				deadline := l.earliestDeadline()
				l.response.SetStatusCode(deadline.status)
				l.response.Header.SetContentType("text/plain; charset=utf-8")
				l.response.SetBodyString(deadline.message)
			}
			for _, scope := range slices.Backward(l.scopes) {
				l.finishScope(scope, &l.response, false)
			}
			l.ctx.TimeoutErrorWithResponse(&l.response)
			completed = true
			return
		}
	}
}

// A callback panic must not release the live context back to the server while
// the worker still holds it. Keep the original panic, but detach the response
// and release every scope even if another callback also panics during cleanup.
func (l *timeoutLayer) abort() {
	l.timedOut = true
	l.aborted = true
	l.response.Reset()
	l.response.SetStatusCode(fasthttp.StatusInternalServerError)
	l.response.SetBodyString(fasthttp.StatusMessage(fasthttp.StatusInternalServerError))
	for _, scope := range slices.Backward(l.scopes) {
		func() {
			defer func() { _ = recover() }()
			l.finishScope(scope, &l.response, false)
		}()
	}
	l.ctx.TimeoutErrorWithResponse(&l.response)
}

func (l *timeoutLayer) earliestDeadline() *timeoutDeadline {
	earliest := l.deadlines[0]
	for _, deadline := range l.deadlines[1:] {
		if deadline.at.Before(earliest.at) {
			earliest = deadline
		}
	}
	return earliest
}

func (l *timeoutLayer) finishScope(scope *handlerScope, response *fasthttp.Response, restore bool) {
	if restore {
		defer scope.restore()
	}
	defer scope.finishSpan(response)
	scope.finishAppSec(response)
}

func (l *timeoutLayer) finishOuter(scope *handlerScope) {
	if l.timedOut {
		return
	}
	// The worker has exited, so the resource namer can read what the handler
	// stored in the context.
	scope.setResource()
	l.finishScope(scope, &l.ctx.Response, true)
}

var timerPool sync.Pool

func acquireTimer(d time.Duration) *time.Timer {
	if timer, ok := timerPool.Get().(*time.Timer); ok {
		timer.Reset(d)
		return timer
	}
	return time.NewTimer(d)
}

// releaseTimer puts timer back in the pool. A timer from the pool must not
// deliver a value from its previous use. Since Go 1.23, Stop guarantees this.
// The drain is for programs that set GODEBUG=asynctimerchan=1.
func releaseTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timerPool.Put(timer)
}
