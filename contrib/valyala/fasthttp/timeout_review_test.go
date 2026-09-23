// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttp

import (
	"context"
	"net"
	"net/http"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func timeoutReviewContext() *fasthttp.RequestCtx {
	var req fasthttp.Request
	req.SetRequestURI("http://example.test/")
	ctx := new(fasthttp.RequestCtx)
	ctx.Init(&req, &net.TCPAddr{}, nil)
	return ctx
}

func waitTimeoutWorker(t *testing.T, exited <-chan struct{}) {
	t.Helper()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout worker did not finish")
	}
}

func TestTimeoutHandlerSequentialCalls(t *testing.T) {
	startAppSecRegressionRules(t)
	for _, reuse := range []bool{false, true} {
		name := "different-wrappers"
		if reuse {
			name = "same-wrapper"
		}
		t.Run(name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			first := TimeoutHandler(WrapHandler(func(ctx *fasthttp.RequestCtx) {
				ctx.SetStatusCode(http.StatusCreated)
			}), time.Second, "timeout", WithTimeoutConcurrency(1))
			second := first
			if !reuse {
				second = TimeoutHandler(WrapHandler(func(ctx *fasthttp.RequestCtx) {
					ctx.SetStatusCode(http.StatusAccepted)
				}), time.Second, "timeout", WithTimeoutConcurrency(1))
			}
			ctx := timeoutReviewContext()
			WrapHandler(func(ctx *fasthttp.RequestCtx) {
				first(ctx)
				second(ctx)
				ctx.SetStatusCode(http.StatusNoContent)
			})(ctx)
			spans := timeoutServerSpans(mt)
			require.Len(t, spans, 3)
			require.Equal(t, "201", spans[0].Tag(ext.HTTPCode))
			if reuse {
				require.Equal(t, "201", spans[1].Tag(ext.HTTPCode))
			} else {
				require.Equal(t, "202", spans[1].Tag(ext.HTTPCode))
			}
			require.Equal(t, "204", spans[2].Tag(ext.HTTPCode))
			require.Equal(t, spans[2].SpanID(), spans[0].ParentID())
			require.Equal(t, spans[2].SpanID(), spans[1].ParentID())
			require.Nil(t, ctx.UserValue(handlerScopeKey{}))
			require.Nil(t, ctx.UserValue(timeoutContextKey{}))
			_, found := dyngo.FromContext(ctx)
			require.False(t, found)
		})
	}
}

func TestTimeoutHandlerFirstScopeCoversWorker(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	first := WrapHandler(func(*fasthttp.RequestCtx) {}, WithResourceNamer(func(*fasthttp.RequestCtx) string { return "first" }))
	second := WrapHandler(func(*fasthttp.RequestCtx) {}, WithResourceNamer(func(*fasthttp.RequestCtx) string { return "second" }))
	TimeoutHandler(func(ctx *fasthttp.RequestCtx) {
		first(ctx)
		second(ctx)
	}, time.Second, "timeout")(timeoutReviewContext())
	spans := timeoutServerSpans(mt)
	require.Len(t, spans, 2)
	require.Equal(t, "second", spans[0].Tag(ext.ResourceName))
	require.Equal(t, "first", spans[1].Tag(ext.ResourceName))
	require.Equal(t, spans[1].SpanID(), spans[0].ParentID())
}

func TestTimeoutHandlerKeepsEarlyBlock(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/blocking.json")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
	testutils.StartAppSec(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	ctx := timeoutReviewContext()
	ctx.Request.Header.Set("X-Forwarded-For", "1.2.3.4")
	stop, cancel := context.WithCancel(context.Background())
	defer cancel()
	exited := make(chan (<-chan struct{}), 1)
	called := false
	TimeoutHandler(func(ctx *fasthttp.RequestCtx) {
		exited <- ctx.UserValue(timeoutContextKey{}).(*timeoutLayer).workerExited
		WrapHandler(func(*fasthttp.RequestCtx) { called = true })(ctx)
		// Hold the worker after the block so the timeout must select a response.
		<-stop.Done()
	}, 50*time.Millisecond, "timeout")(ctx)
	response := ctx.LastTimeoutErrorResponse()
	require.NotNil(t, response)
	require.Equal(t, http.StatusForbidden, response.StatusCode())
	require.Equal(t, "application/json", string(response.Header.ContentType()))
	cancel()
	waitTimeoutWorker(t, <-exited)
	require.False(t, called)
	spans := timeoutServerSpans(mt)
	require.Len(t, spans, 1)
	require.Equal(t, "403", spans[0].Tag(ext.HTTPCode))
	require.Contains(t, spans[0].Tag("_dd.appsec.json"), "blk-001-001")
}

func TestTimeoutHandlerMonitoringEndsAtDeadline(t *testing.T) {
	startAppSecRegressionRules(t)
	for _, monitor := range []struct {
		name string
		call func(context.Context, any) error
	}{
		{"request-body", appsec.MonitorParsedHTTPBody},
		{"response-body", appsec.MonitorHTTPResponseBody},
	} {
		t.Run(monitor.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			ctx := timeoutReviewContext()
			stop, cancel := context.WithCancel(context.Background())
			defer cancel()
			exited := make(chan (<-chan struct{}), 1)
			monitorError := make(chan error, 1)
			WrapHandler(TimeoutHandler(func(ctx *fasthttp.RequestCtx) {
				exited <- ctx.UserValue(timeoutContextKey{}).(*timeoutLayer).workerExited
				<-stop.Done()
				monitorError <- monitor.call(ctx, map[string]any{"value": "attack"})
			}, 20*time.Millisecond, "timeout"))(ctx)
			require.Equal(t, http.StatusRequestTimeout, ctx.LastTimeoutErrorResponse().StatusCode())
			cancel()
			waitTimeoutWorker(t, <-exited)
			require.NoError(t, <-monitorError)
			spans := timeoutServerSpans(mt)
			require.Len(t, spans, 1)
			require.Nil(t, spans[0].Tag("_dd.appsec.json"))
		})
	}
}

func TestTimeoutHandlerDoesNotRepeatHandledPanic(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	ctx := timeoutReviewContext()
	exited := make(chan (<-chan struct{}), 1)
	handler := TimeoutHandler(WrapHandler(func(ctx *fasthttp.RequestCtx) {
		exited <- ctx.UserValue(timeoutContextKey{}).(*timeoutLayer).workerExited
		panic("handler panic")
	}, WithStatusCheck(func(int) bool { panic("finish panic") })), time.Second, "timeout")
	require.PanicsWithValue(t, "finish panic", func() { handler(ctx) })
	waitTimeoutWorker(t, <-exited)
	require.Nil(t, ctx.UserValue(timeoutContextKey{}))
	require.Nil(t, ctx.UserValue(handlerScopeKey{}))
}

func TestTimeoutExchangeKeepsQueuedReply(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	for range 100 {
		layer := &timeoutLayer{
			events:  make(chan timeoutEvent),
			stopped: make(chan struct{}),
		}
		result := make(chan *timeoutReply, 1)
		go func() { result <- layer.exchange(timeoutEvent{}) }()
		// Let the worker block on its send before the owner receives it.
		runtime.Gosched()
		event := <-layer.events
		want := &timeoutReply{scope: &handlerScope{handled: true}}
		event.reply <- want
		// With one processor, the owner closes stopped before the worker
		// resumes. Both channels are then ready, but the reply must win.
		layer.stop()
		require.Same(t, want, <-result)
	}
}

func TestTimeoutHandlerSequentialWorkerLimit(t *testing.T) {
	var exited <-chan struct{}
	handler := TimeoutHandler(func(ctx *fasthttp.RequestCtx) {
		exited = ctx.UserValue(timeoutContextKey{}).(*timeoutLayer).workerExited
		ctx.SetStatusCode(http.StatusCreated)
	}, time.Second, "timeout", WithTimeoutConcurrency(1))
	for range 2000 {
		ctx := timeoutReviewContext()
		handler(ctx)
		require.Equal(t, http.StatusCreated, ctx.Response.StatusCode())
		select {
		case <-exited:
		default:
			t.Fatal("normal return left a worker slot occupied")
		}
	}
}

func TestWithTimeoutConcurrencyRequiresPositiveLimit(t *testing.T) {
	for _, limit := range []int{0, -1} {
		require.PanicsWithValue(t, "fasthttp: timeout concurrency must be positive", func() {
			WithTimeoutConcurrency(limit)
		})
	}
}
