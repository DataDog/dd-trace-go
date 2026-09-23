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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
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
			require.Len(t, timeoutServerSpans(mt), 1)
		})
	}
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
