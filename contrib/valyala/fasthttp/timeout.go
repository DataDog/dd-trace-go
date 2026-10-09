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
// The worker limit defaults to 1,024 and is separate from
// Server.Concurrency. A timed-out worker keeps its slot until h returns. Use
// WithTimeoutConcurrency to set a limit for your server. Excess requests
// receive 429 Too Many Requests, not msg. A non-positive timeout disables this wrapper.
//
// Do not enable span pooling with [tracer.WithSpanPool] when using these wrappers.
// A timed-out worker can otherwise use a span recycled for another request.
// Span pooling is disabled by default.
func TimeoutHandler(h fasthttp.RequestHandler, timeout time.Duration, msg string, opts ...TimeoutOption) fasthttp.RequestHandler {
	return TimeoutWithCodeHandler(h, timeout, msg, fasthttp.StatusRequestTimeout, opts...)
}

// TimeoutWithCodeHandler is TimeoutHandler with a custom timeout status code.
func TimeoutWithCodeHandler(h fasthttp.RequestHandler, timeout time.Duration, msg string, statusCode int, opts ...TimeoutOption) fasthttp.RequestHandler {
	cfg := timeoutOptions{concurrency: defaultTimeoutConcurrency}
	for _, option := range opts {
		option(&cfg)
	}
	return timeoutWithCodeHandler(h, timeout, msg, statusCode, cfg.concurrency)
}

// defaultTimeoutConcurrency is the worker limit of one timeout wrapper when
// WithTimeoutConcurrency is not given. It is less than
// fasthttp.DefaultConcurrency on purpose: a timed-out worker keeps its handler
// goroutine and RequestCtx until the handler returns, also after the server
// has released its own concurrency slot.
const defaultTimeoutConcurrency = 1024

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
			ctx.Error(fasthttp.StatusMessage(fasthttp.StatusTooManyRequests), fasthttp.StatusTooManyRequests)
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
	// panicked tells that the handler of a finished scope did not return
	// normally. The owner records it, so only the owner writes the scope.
	panicked bool
}

// # Design of the timeout layer
//
// fasthttp.TimeoutHandler runs the handler in a worker goroutine. At the
// deadline, it sends the timeout response and returns, but the worker
// continues to use the same RequestCtx. Tracing and AppSec must read the
// response and remove their context values when the request ends. With the
// native wrapper, this causes data races, and the span or the WAF can see a
// response that the client does not get. This file replaces that wrapper. Each
// part below has a reason:
//
//  1. One owner goroutine. The goroutine that calls the wrapper (the owner)
//     starts and finishes all spans and AppSec operations. The worker runs only
//     the application handler. Thus no lock is necessary for the scope state:
//     only the owner changes it. If WrapHandler is outside the timeout
//     wrapper, the layer adopts its scopes, so that the owner can finish them
//     with the timeout response at the deadline.
//  2. The events channel. WrapHandler can be inside the timeout wrapper. Then
//     it runs on the worker, but it must not start or finish a scope itself.
//     It sends an event to the owner (exchange) and waits for the reply. While
//     it waits, the worker is paused, so the owner can read the RequestCtx
//     safely (for example, to run resource namers).
//  3. The stopped channel. At the deadline, the owner stops to reply. A worker
//     that sends an event after this point must not block forever. exchange
//     returns false, and the worker then cleans up its own values.
//  4. A separate timeout response. At the deadline, the worker can still write
//     ctx.Response. The owner does not touch it. It writes l.response, lets
//     AppSec replace it (blocking), and sends it with
//     RequestCtx.TimeoutErrorWithResponse.
//  5. blockedResponse. If AppSec blocks before the handler runs, the owner
//     keeps a copy of the blocking response. If the request then times out,
//     the client gets the block, not the timeout message.
//  6. Context values are restored only after the worker returns. The worker
//     can read them until then. After a timeout, the worker restores them.
//  7. Nested timeout wrappers use the outer layer. They only add a deadline;
//     the earliest active deadline wins. Thus there is always one worker and
//     one owner for each request.
//  8. The worker limit (workers channel). A timed-out worker keeps its
//     goroutine and RequestCtx until the handler returns. Without a limit, a
//     slow handler can make the number of goroutines increase without bound.
//  9. abort. If a callback on the owner panics (for example, a resource namer),
//     the owner must not give the live RequestCtx back to the server while the
//     worker still uses it. abort sends a detached 500 response and finishes
//     all scopes.
//  10. Timer pool and drain. They remove one allocation for each request, and
//     they prevent an old tick from expiring a new deadline when the program
//     uses asynchronous timer channels (before Go 1.23 semantics).
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
	// Do not recover the panic: the worker goroutine handles it.
	returned := false
	defer func() {
		if !l.exchange(timeoutEvent{scope: scope, panicked: !returned}) {
			// The owner has finished against a detached response. Only this
			// worker may now change the live context's user values.
			scope.restore()
		}
	}()
	defer activateTimeoutWorker(ctx, scope)()
	if !scope.handled {
		h(ctx)
	}
	returned = true
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
				if scope.wroteBlock() && l.blockedResponse == nil {
					// The worker is paused here. Preserve an early block before
					// application code can change the live response again. A
					// block that failed (because the application selected a
					// timeout response before) wrote no response to keep.
					l.blockedResponse = new(fasthttp.Response)
					l.ctx.Response.CopyTo(l.blockedResponse)
				}
				*event.started = scope
				event.reply <- struct{}{}
			case event.scope != nil:
				if event.panicked {
					event.scope.panicked = true
				}
				if event.scope != l.rootWorker {
					// The worker waits for this reply, so the resource namer
					// can read what the handler stored in the context.
					namerPanic := event.scope.setFinalResource()
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
					// The scope is finished. A namer panic now aborts the
					// request, as other owner callback panics do. If the
					// handler panicked, its panic continues in the worker.
					event.scope.raiseNamerPanic(namerPanic)
				}
				event.reply <- struct{}{}
			case event.deadline != nil:
				l.updateDeadline(timer, event)
				event.reply <- struct{}{}
			}
		case <-l.workerDone:
			l.workerPanicHandled = true
			var namerPanic any
			// No application code can now access the live response or values.
			if l.rootWorker != nil {
				if l.panicked {
					// The root scope covers the whole worker. Code after its
					// handler can panic, so its status is not the result.
					l.rootWorker.panicked = true
				}
				namerPanic = l.rootWorker.setFinalResource()
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
			// The worker has exited, so the namer panic does not need abort.
			// If application code in the worker recovered a panic of the
			// root handler, discard the namer panic, as WrapHandler does.
			if l.rootWorker != nil {
				l.rootWorker.raiseNamerPanic(namerPanic)
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

// updateDeadline adds or removes the deadline of event. Then it sets timer to
// the earliest active deadline. A removed deadline can have expired already,
// so resetTimer must discard its tick.
func (l *timeoutLayer) updateDeadline(timer *time.Timer, event timeoutEvent) {
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
	resetTimer(timer, time.Until(l.earliestDeadline().at))
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
	namerPanic := scope.setFinalResource()
	l.finishScope(scope, &l.ctx.Response, true)
	scope.raiseNamerPanic(namerPanic)
}

var timerPool sync.Pool

func acquireTimer(d time.Duration) *time.Timer {
	if timer, ok := timerPool.Get().(*time.Timer); ok {
		timer.Reset(d)
		return timer
	}
	return time.NewTimer(d)
}

// resetTimer changes the expiry of timer to d. The timer must not deliver a
// value from an earlier expiry. With synchronous timer channels (the default
// since Go 1.23), Stop guarantees this. The drain is for programs that use
// asynchronous timer channels: GODEBUG=asynctimerchan=1, or a main module
// that declares a Go version before 1.23. In those programs, an expired timer
// keeps its value in the channel until a receive.
func resetTimer(timer *time.Timer, d time.Duration) {
	stopTimer(timer)
	timer.Reset(d)
}

// releaseTimer puts timer back in the pool. A timer from the pool must not
// deliver a value from its previous use.
func releaseTimer(timer *time.Timer) {
	stopTimer(timer)
	timerPool.Put(timer)
}

// stopTimer stops timer and removes a value that is in its channel. Only the
// owner of the timer receives from the channel, so the drain cannot block.
func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}
