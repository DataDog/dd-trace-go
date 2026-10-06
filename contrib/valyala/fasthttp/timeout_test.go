// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttp

import (
	"context"
	"io"
	"net"
	"net/http"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func serveTimeoutTest(t *testing.T, handler fasthttp.RequestHandler) (*http.Client, string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &fasthttp.Server{Handler: handler}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, srv.ShutdownWithContext(ctx))
		require.NoError(t, <-serveErr)
	})
	client := &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	wantSpans := 1
	// Orchestrion adds a server wrapper in addition to the explicit wrapper.
	if _, ok := reflect.TypeFor[fasthttp.Server]().FieldByName("DD_Instrumented"); ok {
		wantSpans++
	}
	return client, "http://" + ln.Addr().String(), wantSpans
}

func timeoutServerSpans(mt mocktracer.Tracer) []*mocktracer.Span {
	var spans []*mocktracer.Span
	for _, span := range mt.FinishedSpans() {
		if span.Tag(ext.Component) == "valyala/fasthttp" {
			spans = append(spans, span)
		}
	}
	return spans
}

func TestTimeoutHandlerWrapperOrders(t *testing.T) {
	startAppSecRegressionRules(t)

	for _, outerWrappers := range []int{0, 1, 2} {
		for _, timeoutStatus := range []int{http.StatusRequestTimeout, http.StatusServiceUnavailable, http.StatusInternalServerError} {
			t.Run("outer-wrappers="+strconv.Itoa(outerWrappers)+"/status="+strconv.Itoa(timeoutStatus), func(t *testing.T) {
				mt := mocktracer.Start()
				t.Cleanup(mt.Stop)
				stop, cancel := context.WithCancel(context.Background())
				defer cancel()
				workerDone := make(chan struct{})
				afterResponse := make(chan struct{})
				afterTimeoutWrite := make(chan struct{})
				operationFound := make(chan bool, 1)
				monitorErrors := make(chan error, 1)
				requestContext := make(chan *fasthttp.RequestCtx, 1)
				workerExited := make(chan (<-chan struct{}), 1)
				handler := fasthttp.RequestHandler(func(ctx *fasthttp.RequestCtx) {
					defer close(workerDone)
					_, found := dyngo.FindOperation[httpsec.HandlerOperation](ctx)
					operationFound <- found
					requestContext <- ctx
					workerExited <- ctx.UserValue(timeoutContextKey{}).(*timeoutLayer).workerExited
					var monitorErr error
					key := 0
					wroteAfterTimeout := false
					for stop.Err() == nil {
						ctx.SetUserValue(key, key)
						ctx.RemoveUserValue(key - 1)
						key++
						_ = ctx.Value("application-value")
						ctx.Response.Header.Set("X-Handler", "still-running")
						ctx.SetStatusCode(http.StatusCreated)
						ctx.SetBodyString("handler response")
						if err := appsec.MonitorParsedHTTPBody(ctx, map[string]any{"value": "clean"}); err != nil {
							monitorErr = err
						}
						if !wroteAfterTimeout {
							select {
							case <-afterResponse:
								ctx.SetUserValue("after-timeout", true)
								ctx.SetBodyString("written after the timeout response")
								wroteAfterTimeout = true
								close(afterTimeoutWrite)
							default:
							}
						}
						runtime.Gosched()
					}
					monitorErrors <- monitorErr
				})
				if outerWrappers == 0 {
					handler = WrapHandler(handler)
				}
				wantStatus := timeoutStatus
				if timeoutStatus == http.StatusRequestTimeout {
					handler = TimeoutHandler(handler, 50*time.Millisecond, "request timed out")
				} else {
					handler = TimeoutWithCodeHandler(handler, 50*time.Millisecond, "request timed out", timeoutStatus)
				}
				if timeoutStatus == http.StatusInternalServerError {
					wantStatus = http.StatusTeapot
				}
				for range outerWrappers {
					handler = WrapHandler(handler)
				}
				client, url, wantSpans := serveTimeoutTest(t, handler)
				if outerWrappers > 1 {
					wantSpans += outerWrappers - 1
				}
				res, err := client.Get(url)
				require.NoError(t, err)
				defer res.Body.Close()
				body, err := io.ReadAll(res.Body)
				require.NoError(t, err)
				require.Equal(t, wantStatus, res.StatusCode)
				if wantStatus == http.StatusTeapot {
					require.NotContains(t, string(body), "request timed out")
					require.Equal(t, "application/json", res.Header.Get("Content-Type"))
				} else {
					require.Equal(t, "request timed out", string(body))
				}
				require.Empty(t, res.Header.Get("X-Handler"))
				require.True(t, <-operationFound)
				close(afterResponse)
				select {
				case <-afterTimeoutWrite:
				case <-time.After(5 * time.Second):
					t.Fatal("worker did not continue after the timeout response")
				}
				cancel()
				select {
				case <-workerDone:
				case <-time.After(5 * time.Second):
					t.Fatal("application handler did not stop")
				}
				select {
				case <-<-workerExited:
				case <-time.After(5 * time.Second):
					t.Fatal("timeout worker cleanup did not finish")
				}
				require.NoError(t, <-monitorErrors)
				ctx := <-requestContext
				require.Nil(t, ctx.UserValue(timeoutContextKey{}))
				require.Nil(t, ctx.UserValue(handlerScopeKey{}))
				_, found := dyngo.FromContext(ctx)
				require.False(t, found)
				require.Eventually(t, func() bool { return len(timeoutServerSpans(mt)) == wantSpans }, 5*time.Second, time.Millisecond)
				for _, span := range timeoutServerSpans(mt) {
					require.Equal(t, strconv.Itoa(wantStatus), span.Tag(ext.HTTPCode))
					if wantStatus == http.StatusServiceUnavailable {
						require.Equal(t, "503: Service Unavailable", span.Tag(ext.ErrorMsg))
					} else {
						require.Nil(t, span.Tag(ext.ErrorMsg))
					}
				}
			})
		}
	}
}

func TestTimeoutHandlerAppSecBlocking(t *testing.T) {
	startAppSecRegressionRules(t)

	for _, outside := range []bool{false, true} {
		t.Run("tracing-outside="+strconv.FormatBool(outside), func(t *testing.T) {
			mt := mocktracer.Start()
			t.Cleanup(mt.Stop)
			monitorErr := make(chan error, 1)
			handler := fasthttp.RequestHandler(func(ctx *fasthttp.RequestCtx) {
				monitorErr <- appsec.MonitorParsedHTTPBody(ctx, map[string]any{"value": "attack"})
				ctx.SetBodyString("handler response")
			})
			if !outside {
				handler = WrapHandler(handler)
			}
			handler = TimeoutHandler(handler, 5*time.Second, "request timed out")
			if outside {
				handler = WrapHandler(handler)
			}
			client, url, wantSpans := serveTimeoutTest(t, handler)
			res, err := client.Get(url)
			require.NoError(t, err)
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.Error(t, <-monitorErr)
			require.Equal(t, http.StatusTeapot, res.StatusCode)
			require.NotContains(t, string(body), "handler response")
			require.Eventually(t, func() bool { return len(timeoutServerSpans(mt)) == wantSpans }, 5*time.Second, time.Millisecond)
			var events []string
			for _, span := range timeoutServerSpans(mt) {
				require.Equal(t, "418", span.Tag(ext.HTTPCode))
				if event, ok := span.Tag("_dd.appsec.json").(string); ok {
					events = append(events, event)
				}
			}
			require.Len(t, events, 1)
			require.Contains(t, events[0], "server.request.body")
		})
	}
}

func TestTimeoutHandlerNestedTracing(t *testing.T) {
	startAppSecRegressionRules(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var req fasthttp.Request
	req.SetRequestURI("http://example.test/")
	var ctx fasthttp.RequestCtx
	ctx.Init(&req, &net.TCPAddr{}, nil)
	ctx.SetUserValue("application-value", "preserved")
	valid := make(chan bool, 1)
	TimeoutHandler(WrapHandler(func(ctx *fasthttp.RequestCtx) {
		outer, found := dyngo.FindOperation[httpsec.HandlerOperation](ctx)
		ok := found
		for range 10 {
			WrapHandler(func(ctx *fasthttp.RequestCtx) {
				inner, found := dyngo.FindOperation[httpsec.HandlerOperation](ctx)
				ok = ok && found && inner != outer
			})(ctx)
			restored, found := dyngo.FindOperation[httpsec.HandlerOperation](ctx)
			ok = ok && found && restored == outer
			layer := ctx.UserValue(timeoutContextKey{}).(*timeoutLayer)
			ok = ok && len(layer.scopes) == 1
		}
		if _, instrumented := reflect.TypeFor[fasthttp.Server]().FieldByName("DD_Instrumented"); instrumented {
			implicit, found := dyngo.FindOperation[httpsec.HandlerOperation](nil)
			ok = ok && found && implicit == outer
		}
		valid <- ok
		ctx.SetBodyString("done")
	}), 5*time.Second, "timeout")(&ctx)
	require.True(t, <-valid)
	require.Equal(t, "done", string(ctx.Response.Body()))
	require.Equal(t, "preserved", ctx.UserValue("application-value"))
	require.Nil(t, ctx.UserValue(timeoutContextKey{}))
	require.Nil(t, ctx.UserValue(handlerScopeKey{}))
	_, found := dyngo.FromContext(&ctx)
	require.False(t, found)
	require.Len(t, timeoutServerSpans(mt), 11)
}

func TestTimeoutHandlerPanic(t *testing.T) {
	startAppSecRegressionRules(t)
	for _, outside := range []bool{false, true} {
		t.Run("tracing-outside="+strconv.FormatBool(outside), func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			var req fasthttp.Request
			req.SetRequestURI("http://example.test/")
			var ctx fasthttp.RequestCtx
			ctx.Init(&req, &net.TCPAddr{}, nil)
			handler := fasthttp.RequestHandler(func(*fasthttp.RequestCtx) { panic("handler panic") })
			if !outside {
				handler = WrapHandler(handler)
			}
			handler = TimeoutHandler(handler, 5*time.Second, "timeout")
			if outside {
				handler = WrapHandler(handler)
			}
			require.PanicsWithValue(t, "handler panic", func() { handler(&ctx) })
			require.Nil(t, ctx.UserValue(timeoutContextKey{}))
			require.Nil(t, ctx.UserValue(handlerScopeKey{}))
			_, found := dyngo.FromContext(&ctx)
			require.False(t, found)
			spans := timeoutServerSpans(mt)
			require.Len(t, spans, 1)
			// The live response still has fasthttp's default status 200, which
			// is not the result of the handler.
			require.Nil(t, spans[0].Tag(ext.HTTPCode))
			require.Equal(t, errHandlerPanic.Error(), spans[0].Tag(ext.ErrorMsg))
		})
	}
}

// A nested scope in the timeout worker finishes on the owner. It must also
// report the panic and not the live response status.
func TestTimeoutHandlerNestedPanic(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	var req fasthttp.Request
	req.SetRequestURI("http://example.test/")
	var ctx fasthttp.RequestCtx
	ctx.Init(&req, &net.TCPAddr{}, nil)
	handler := TimeoutHandler(WrapHandler(WrapHandler(func(*fasthttp.RequestCtx) {
		panic("handler panic")
	})), 5*time.Second, "timeout")
	require.PanicsWithValue(t, "handler panic", func() { handler(&ctx) })
	spans := timeoutServerSpans(mt)
	require.Len(t, spans, 2)
	for _, span := range spans {
		require.Nil(t, span.Tag(ext.HTTPCode))
		require.Equal(t, errHandlerPanic.Error(), span.Tag(ext.ErrorMsg))
	}
}

// The first traced scope covers the whole worker. A panic after its handler
// returns must also hide the live response status.
func TestTimeoutHandlerPanicAfterRootScope(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	var req fasthttp.Request
	req.SetRequestURI("http://example.test/")
	var ctx fasthttp.RequestCtx
	ctx.Init(&req, &net.TCPAddr{}, nil)
	traced := WrapHandler(func(*fasthttp.RequestCtx) {})
	handler := TimeoutHandler(func(ctx *fasthttp.RequestCtx) {
		traced(ctx)
		panic("handler panic")
	}, 5*time.Second, "timeout")
	require.PanicsWithValue(t, "handler panic", func() { handler(&ctx) })
	spans := timeoutServerSpans(mt)
	require.Len(t, spans, 1)
	require.Nil(t, spans[0].Tag(ext.HTTPCode))
	require.Equal(t, errHandlerPanic.Error(), spans[0].Tag(ext.ErrorMsg))
}

func TestTimeoutHandlerResourceNamerPanic(t *testing.T) {
	startAppSecRegressionRules(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var req fasthttp.Request
	req.SetRequestURI("http://example.test/")
	var ctx fasthttp.RequestCtx
	ctx.Init(&req, &net.TCPAddr{}, nil)
	workerExited := make(chan (<-chan struct{}), 1)
	called := false
	handler := TimeoutHandler(WrapHandler(func(*fasthttp.RequestCtx) {
		called = true
	}, WithResourceNamer(func(ctx *fasthttp.RequestCtx) string {
		workerExited <- ctx.UserValue(timeoutContextKey{}).(*timeoutLayer).workerExited
		panic("resource namer panic")
	})), 5*time.Second, "timeout")
	require.PanicsWithValue(t, "resource namer panic", func() { handler(&ctx) })
	select {
	case <-<-workerExited:
	case <-time.After(5 * time.Second):
		t.Fatal("worker was not released after resource namer panic")
	}
	require.False(t, called)
	require.Nil(t, ctx.UserValue(timeoutContextKey{}))
	require.Nil(t, ctx.UserValue(handlerScopeKey{}))
	_, found := dyngo.FromContext(&ctx)
	require.False(t, found)
	require.Len(t, timeoutServerSpans(mt), 1)
}

func TestTimeoutHandlerNonPositiveDuration(t *testing.T) {
	for _, duration := range []time.Duration{0, -time.Second} {
		t.Run(duration.String(), func(t *testing.T) {
			var ctx fasthttp.RequestCtx
			called := false
			TimeoutHandler(func(got *fasthttp.RequestCtx) {
				require.Same(t, &ctx, got)
				require.Nil(t, got.UserValue(timeoutContextKey{}))
				called = true
			}, duration, "timeout")(&ctx)
			require.True(t, called)
		})
	}
}

func TestTimeoutHandlerNestedDeadline(t *testing.T) {
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	stop, cancel := context.WithCancel(context.Background())
	defer cancel()
	workerExited := make(chan (<-chan struct{}), 1)
	handler := TimeoutWithCodeHandler(func(ctx *fasthttp.RequestCtx) {
		workerExited <- ctx.UserValue(timeoutContextKey{}).(*timeoutLayer).workerExited
		<-stop.Done()
	}, 20*time.Millisecond, "inner timeout", http.StatusGatewayTimeout)
	handler = WrapHandler(TimeoutHandler(handler, 5*time.Second, "outer timeout"))
	client, url, wantSpans := serveTimeoutTest(t, handler)
	res, err := client.Get(url)
	require.NoError(t, err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusGatewayTimeout, res.StatusCode)
	require.Equal(t, "inner timeout", string(body))
	cancel()
	select {
	case <-<-workerExited:
	case <-time.After(5 * time.Second):
		t.Fatal("nested timeout worker did not stop")
	}
	require.Eventually(t, func() bool { return len(timeoutServerSpans(mt)) == wantSpans }, 5*time.Second, time.Millisecond)
	for _, span := range timeoutServerSpans(mt) {
		require.Equal(t, "504", span.Tag(ext.HTTPCode))
	}
}

func TestTimeoutHandlerWithoutTracing(t *testing.T) {
	for _, timedOut := range []bool{false, true} {
		t.Run(strconv.FormatBool(timedOut), func(t *testing.T) {
			var req fasthttp.Request
			req.SetRequestURI("http://example.test/")
			var ctx fasthttp.RequestCtx
			ctx.Init(&req, &net.TCPAddr{}, nil)
			stop, cancel := context.WithCancel(context.Background())
			defer cancel()
			workerExited := make(chan (<-chan struct{}), 1)
			TimeoutHandler(func(ctx *fasthttp.RequestCtx) {
				workerExited <- ctx.UserValue(timeoutContextKey{}).(*timeoutLayer).workerExited
				if timedOut {
					<-stop.Done()
				} else {
					ctx.SetBodyString("completed")
				}
			}, 20*time.Millisecond, "timeout")(&ctx)
			if timedOut {
				response := ctx.LastTimeoutErrorResponse()
				require.NotNil(t, response)
				require.Equal(t, http.StatusRequestTimeout, response.StatusCode())
				require.Equal(t, "timeout", string(response.Body()))
			} else {
				require.Nil(t, ctx.LastTimeoutErrorResponse())
				require.Equal(t, "completed", string(ctx.Response.Body()))
			}
			cancel()
			select {
			case <-<-workerExited:
			case <-time.After(5 * time.Second):
				t.Fatal("untraced timeout worker did not stop")
			}
			require.Nil(t, ctx.UserValue(timeoutContextKey{}))
		})
	}
}

func TestTimeoutHandlerNestedCompletion(t *testing.T) {
	var req fasthttp.Request
	req.SetRequestURI("http://example.test/")
	var ctx fasthttp.RequestCtx
	ctx.Init(&req, &net.TCPAddr{}, nil)
	deadlines := make(chan int, 1)
	inner := TimeoutHandler(func(ctx *fasthttp.RequestCtx) { ctx.SetBodyString("completed") }, 4*time.Second, "inner timeout")
	TimeoutHandler(func(ctx *fasthttp.RequestCtx) {
		inner(ctx)
		layer := ctx.UserValue(timeoutContextKey{}).(*timeoutLayer)
		deadlines <- len(layer.deadlines)
	}, 5*time.Second, "outer timeout")(&ctx)
	require.Equal(t, 1, <-deadlines)
	require.Nil(t, ctx.LastTimeoutErrorResponse())
	require.Equal(t, "completed", string(ctx.Response.Body()))
	require.Nil(t, ctx.UserValue(timeoutContextKey{}))
}

func TestTimeoutHandlerWorkerLimit(t *testing.T) {
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	stop, cancel := context.WithCancel(context.Background())
	defer cancel()
	workerExited := make(chan (<-chan struct{}), 1)
	handler := TimeoutWithCodeHandler(func(ctx *fasthttp.RequestCtx) {
		if string(ctx.Path()) == "/fast" {
			ctx.SetBodyString("completed")
			return
		}
		workerExited <- ctx.UserValue(timeoutContextKey{}).(*timeoutLayer).workerExited
		<-stop.Done()
	}, 20*time.Millisecond, "timeout", http.StatusRequestTimeout, WithTimeoutConcurrency(1))
	client, url, _ := serveTimeoutTest(t, WrapHandler(handler))
	request := func(path string, want int) {
		t.Helper()
		res, err := client.Get(url + path)
		require.NoError(t, err)
		defer res.Body.Close()
		_, err = io.Copy(io.Discard, res.Body)
		require.NoError(t, err)
		require.Equal(t, want, res.StatusCode)
	}
	request("/slow", http.StatusRequestTimeout)
	request("/fast", http.StatusTooManyRequests)
	cancel()
	select {
	case <-<-workerExited:
	case <-time.After(5 * time.Second):
		t.Fatal("worker slot was not released")
	}
	request("/fast", http.StatusOK)
}

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
	calls := 0
	handler := TimeoutHandler(WrapHandler(func(ctx *fasthttp.RequestCtx) {
		exited <- ctx.UserValue(timeoutContextKey{}).(*timeoutLayer).workerExited
		panic("handler panic")
	}, WithResourceNamer(func(*fasthttp.RequestCtx) string {
		// The first call is before the handler; the second is at finish.
		if calls++; calls > 1 {
			panic("finish panic")
		}
		return "resource"
	})), time.Second, "timeout")
	// The namer panic must not replace the handler panic.
	require.PanicsWithValue(t, "handler panic", func() { handler(ctx) })
	waitTimeoutWorker(t, <-exited)
	require.Nil(t, ctx.UserValue(timeoutContextKey{}))
	require.Nil(t, ctx.UserValue(handlerScopeKey{}))
	spans := timeoutServerSpans(mt)
	require.Len(t, spans, 1)
	require.Nil(t, spans[0].Tag(ext.HTTPCode))
	require.Equal(t, errHandlerPanic.Error(), spans[0].Tag(ext.ErrorMsg))
}

// A resource namer can panic after the handler. A panic of the handler must
// continue without change. If the handler returns normally, the namer panic
// continues. In both cases, the span must not report fasthttp's default
// status 200 as a success.
type handlerDoneKey struct{}

// panicAfterHandlerNamer panics only after the handler has stored
// handlerDoneKey. With a timeout wrapper, a namer also runs once before the
// handler.
var panicAfterHandlerNamer = WithResourceNamer(func(ctx *fasthttp.RequestCtx) string {
	if ctx.UserValue(handlerDoneKey{}) != nil {
		panic("namer panic")
	}
	return "resource"
})

func TestResourceNamerPanicAfterHandler(t *testing.T) {
	namer := panicAfterHandlerNamer
	for _, handlerPanics := range []bool{false, true} {
		exited := make(chan (<-chan struct{}), 1)
		app := func(ctx *fasthttp.RequestCtx) {
			if layer, ok := ctx.UserValue(timeoutContextKey{}).(*timeoutLayer); ok {
				exited <- layer.workerExited
			}
			ctx.SetUserValue(handlerDoneKey{}, true)
			if handlerPanics {
				panic("handler panic")
			}
		}
		want, wantErr := "namer panic", errResourceNamerPanic
		if handlerPanics {
			want, wantErr = "handler panic", errHandlerPanic
		}
		for _, tc := range []struct {
			name    string
			handler fasthttp.RequestHandler
		}{
			{"wrap", WrapHandler(app, namer)},
			{"wrap-inside", TimeoutHandler(WrapHandler(app, namer), 5*time.Second, "timeout")},
			{"wrap-outside", WrapHandler(TimeoutHandler(app, 5*time.Second, "timeout"), namer)},
			// The span of the nested scope uses namer. It finishes first.
			{"nested-scope", TimeoutHandler(WrapHandler(WrapHandler(app, namer)), 5*time.Second, "timeout")},
		} {
			name := tc.name + "/handler-returns"
			if handlerPanics {
				name = tc.name + "/handler-panics"
			}
			t.Run(name, func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()
				ctx := timeoutReviewContext()
				require.PanicsWithValue(t, want, func() { tc.handler(ctx) })
				if tc.name != "wrap" {
					// An aborted request keeps its worker until it returns.
					waitTimeoutWorker(t, <-exited)
				}
				require.Nil(t, ctx.UserValue(timeoutContextKey{}))
				require.Nil(t, ctx.UserValue(handlerScopeKey{}))
				spans := timeoutServerSpans(mt)
				require.NotEmpty(t, spans)
				for _, span := range spans {
					// No span reports the live status 200 as the result.
					require.NotEqual(t, "200", span.Tag(ext.HTTPCode))
				}
				require.Nil(t, spans[0].Tag(ext.HTTPCode))
				require.Equal(t, wantErr.Error(), spans[0].Tag(ext.ErrorMsg))
			})
		}
	}
}

func TestTimeoutExchangeKeepsQueuedReply(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	for range 100 {
		layer := &timeoutLayer{
			events:  make(chan timeoutEvent),
			stopped: make(chan struct{}),
		}
		var started *handlerScope
		result := make(chan bool, 1)
		go func() { result <- layer.exchange(timeoutEvent{started: &started}) }()
		// Let the worker block on its send before the owner receives it.
		runtime.Gosched()
		event := <-layer.events
		want := &handlerScope{handled: true}
		*event.started = want
		event.reply <- struct{}{}
		// With one processor, the owner closes stopped before the worker
		// resumes. Both channels are then ready, but the reply must win.
		layer.stop()
		require.True(t, <-result)
		require.Same(t, want, started)
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

// Application code can recover the panic of a traced handler. The namer panic
// after that handler must then be discarded, also when a timeout wrapper
// finishes the span after the worker returns.
func TestRecoveredHandlerPanicDiscardsNamerPanic(t *testing.T) {
	traced := WrapHandler(func(ctx *fasthttp.RequestCtx) {
		ctx.SetUserValue(handlerDoneKey{}, true)
		panic("handler panic")
	}, panicAfterHandlerNamer)
	app := func(ctx *fasthttp.RequestCtx) {
		func() {
			defer func() { require.Equal(t, "handler panic", recover()) }()
			traced(ctx)
		}()
		ctx.SetStatusCode(http.StatusCreated)
	}
	for _, tc := range []struct {
		name    string
		handler fasthttp.RequestHandler
	}{
		{"wrap", app},
		{"timeout", TimeoutHandler(app, 5*time.Second, "timeout")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			ctx := timeoutReviewContext()
			require.NotPanics(t, func() { tc.handler(ctx) })
			require.Equal(t, http.StatusCreated, ctx.Response.StatusCode())
			spans := timeoutServerSpans(mt)
			require.Len(t, spans, 1)
			require.Nil(t, spans[0].Tag(ext.HTTPCode))
			require.Equal(t, errHandlerPanic.Error(), spans[0].Tag(ext.ErrorMsg))
		})
	}
}

// Timed-out workers keep their slots. Without WithTimeoutConcurrency, the
// wrapper must refuse requests when defaultTimeoutConcurrency workers remain.
func TestTimeoutHandlerDefaultWorkerLimit(t *testing.T) {
	// The documented bound. A larger value must fail here, not start more
	// blocked workers.
	require.Equal(t, 1024, defaultTimeoutConcurrency)
	release := make(chan struct{})
	var once sync.Once
	// An assertion failure must not leave workers blocked.
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	exited := make(chan (<-chan struct{}), defaultTimeoutConcurrency)
	handler := TimeoutHandler(func(ctx *fasthttp.RequestCtx) {
		exited <- ctx.UserValue(timeoutContextKey{}).(*timeoutLayer).workerExited
		<-release
	}, time.Millisecond, "timeout")
	for range defaultTimeoutConcurrency {
		ctx := timeoutReviewContext()
		handler(ctx)
		require.Equal(t, fasthttp.StatusRequestTimeout, ctx.LastTimeoutErrorResponse().StatusCode())
	}
	ctx := timeoutReviewContext()
	handler(ctx)
	require.Equal(t, fasthttp.StatusTooManyRequests, ctx.Response.StatusCode())
	once.Do(func() { close(release) })
	for range defaultTimeoutConcurrency {
		waitTimeoutWorker(t, <-exited)
	}
}

func TestWithTimeoutConcurrencyRequiresPositiveLimit(t *testing.T) {
	for _, limit := range []int{0, -1} {
		require.PanicsWithValue(t, "fasthttp: timeout concurrency must be positive", func() {
			WithTimeoutConcurrency(limit)
		})
	}
}

// A resource namer can read a route that the handler stores, as with
// WrapHandler alone, when the request completes before its deadline.
func TestTimeoutHandlerResourceAfterHandler(t *testing.T) {
	namer := WithResourceNamer(func(ctx *fasthttp.RequestCtx) string {
		if route, ok := ctx.UserValue("route").(string); ok {
			return route
		}
		return "unset"
	})
	route := func(name string) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) { ctx.SetUserValue("route", name) }
	}
	for _, tc := range []struct {
		name      string
		handler   fasthttp.RequestHandler
		resources []string
	}{
		{"wrap-outside", WrapHandler(TimeoutHandler(route("/outer"), time.Second, "timeout"), namer), []string{"/outer"}},
		{"wrap-inside", TimeoutHandler(WrapHandler(route("/inner"), namer), time.Second, "timeout"), []string{"/inner"}},
		{"nested-scope", TimeoutHandler(WrapHandler(func(ctx *fasthttp.RequestCtx) {
			WrapHandler(route("/nested"), namer)(ctx)
			ctx.SetUserValue("route", "/root")
		}, namer), time.Second, "timeout"), []string{"/nested", "/root"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			tc.handler(timeoutReviewContext())
			spans := timeoutServerSpans(mt)
			require.Len(t, spans, len(tc.resources))
			for i, resource := range tc.resources {
				require.Equal(t, resource, spans[i].Tag(ext.ResourceName))
			}
		})
	}

	for _, order := range []string{"wrap-inside", "wrap-outside"} {
		t.Run("timed-out/"+order, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			release := make(chan struct{})
			exited := make(chan (<-chan struct{}), 1)
			app := func(ctx *fasthttp.RequestCtx) {
				exited <- ctx.UserValue(timeoutContextKey{}).(*timeoutLayer).workerExited
				// The deadline passes after this change. The span must keep
				// the resource from before the handler started.
				ctx.SetUserValue("route", "/late")
				<-release
			}
			ctx := timeoutReviewContext()
			if order == "wrap-inside" {
				TimeoutHandler(WrapHandler(app, namer), 10*time.Millisecond, "timeout")(ctx)
			} else {
				// The route set before the timeout wrapper is the fallback.
				WrapHandler(func(ctx *fasthttp.RequestCtx) {
					ctx.SetUserValue("route", "/early")
					TimeoutHandler(app, 10*time.Millisecond, "timeout")(ctx)
				}, namer)(ctx)
			}
			spans := timeoutServerSpans(mt)
			require.Len(t, spans, 1)
			if order == "wrap-inside" {
				require.Equal(t, "unset", spans[0].Tag(ext.ResourceName))
			} else {
				require.Equal(t, "/early", spans[0].Tag(ext.ResourceName))
			}
			close(release)
			waitTimeoutWorker(t, <-exited)
			require.Len(t, timeoutServerSpans(mt), 1)
		})
	}
}

// Removing an inner deadline must not end the request at that deadline.
func TestTimeoutHandlerRemovedInnerDeadline(t *testing.T) {
	inner := TimeoutHandler(func(*fasthttp.RequestCtx) {}, 20*time.Millisecond, "inner timeout")
	ctx := timeoutReviewContext()
	TimeoutHandler(func(ctx *fasthttp.RequestCtx) {
		inner(ctx)
		time.Sleep(60 * time.Millisecond)
		ctx.SetStatusCode(http.StatusCreated)
	}, 5*time.Second, "outer timeout")(ctx)
	require.Nil(t, ctx.LastTimeoutErrorResponse())
	require.Equal(t, http.StatusCreated, ctx.Response.StatusCode())
}

// A timer from the pool must not end a later request early. Each first request
// times out, so its timer expires before it goes back to the pool.
func TestTimeoutHandlerPooledTimer(t *testing.T) {
	for range 50 {
		release := make(chan struct{})
		exited := make(chan (<-chan struct{}), 1)
		ctx := timeoutReviewContext()
		TimeoutHandler(func(ctx *fasthttp.RequestCtx) {
			exited <- ctx.UserValue(timeoutContextKey{}).(*timeoutLayer).workerExited
			<-release
		}, time.Millisecond, "timeout")(ctx)
		require.NotNil(t, ctx.LastTimeoutErrorResponse())
		close(release)
		waitTimeoutWorker(t, <-exited)

		ctx = timeoutReviewContext()
		TimeoutHandler(func(ctx *fasthttp.RequestCtx) {
			time.Sleep(2 * time.Millisecond)
			ctx.SetStatusCode(http.StatusCreated)
		}, 5*time.Second, "timeout")(ctx)
		require.Nil(t, ctx.LastTimeoutErrorResponse())
		require.Equal(t, http.StatusCreated, ctx.Response.StatusCode())
	}
}

// BenchmarkTimeoutHandler measures a traced request that completes before its
// deadline. The native fasthttp wrapper does not synchronize with tracing or
// AppSec. Its result shows the added cost; it is not an equivalent option.
func BenchmarkTimeoutHandler(b *testing.B) {
	require.NoError(b, tracer.Start(tracer.WithLogger(testutils.DiscardLogger())))
	defer tracer.Stop()

	app := func(ctx *fasthttp.RequestCtx) { ctx.SetBodyString("ok") }
	for _, bc := range []struct {
		name    string
		handler fasthttp.RequestHandler
	}{
		{"no-timeout", WrapHandler(app)},
		{"native", WrapHandler(fasthttp.TimeoutHandler(app, time.Minute, "timeout"))},
		{"wrap-outside", WrapHandler(TimeoutHandler(app, time.Minute, "timeout"))},
		{"wrap-inside", TimeoutHandler(WrapHandler(app), time.Minute, "timeout")},
	} {
		b.Run(bc.name, func(b *testing.B) {
			var req fasthttp.Request
			req.SetRequestURI("http://example.test/path?query=value")
			req.Header.Set("User-Agent", "benchmark")
			var ctx fasthttp.RequestCtx
			ctx.Init(&req, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}, nil)
			b.ReportAllocs()
			for b.Loop() {
				bc.handler(&ctx)
				ctx.Response.Reset()
			}
		})
	}
}
