// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttp

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func startAppSecRegressionRules(t *testing.T) {
	t.Helper()
	rules := `{
		"version": "2.2",
		"metadata": {"rules_version": "1.0.0"},
		"rules": [{
			"id": "fasthttp-body-response",
			"name": "Block body or response input",
			"tags": {"type": "test", "category": "attack_attempt"},
			"conditions": [{
				"operator": "exact_match",
				"parameters": {
					"inputs": [
						{"address": "server.request.body"},
						{"address": "server.response.body"},
						{"address": "server.response.headers.no_cookies", "key_path": ["x-block"]},
						{"address": "server.response.status"}
					],
					"list": ["attack", "500"]
				}
			}],
			"on_match": ["block-teapot"]
		}],
		"actions": [{
			"id": "block-teapot",
			"type": "block_request",
			"parameters": {"status_code": 418, "type": "json"}
		}]
	}`
	path := filepath.Join(t.TempDir(), "rules.json")
	require.NoError(t, os.WriteFile(path, []byte(rules), 0o600))
	t.Setenv("DD_APPSEC_RULES", path)
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
	testutils.StartAppSec(t)
}

func TestAppSecBodyMonitoring(t *testing.T) {
	startAppSecRegressionRules(t)

	for _, monitor := range []struct {
		name    string
		address string
		body    func(context.Context, any) error
	}{
		{"request", "server.request.body", appsec.MonitorParsedHTTPBody},
		{"response", "server.response.body", appsec.MonitorHTTPResponseBody},
	} {
		for _, attack := range []bool{false, true} {
			t.Run(monitor.name+"/attack="+strconv.FormatBool(attack), func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()
				var req fasthttp.Request
				req.SetRequestURI("http://example.test/")
				var fctx fasthttp.RequestCtx
				fctx.Init(&req, &net.TCPAddr{}, nil)

				var monitorErr error
				WrapHandler(func(fctx *fasthttp.RequestCtx) {
					value := "clean"
					if attack {
						value = "attack"
					}
					monitorErr = monitor.body(fctx, map[string]any{"value": value})
					fctx.SetBodyString("handler response")
				})(&fctx)

				spans := mt.FinishedSpans()
				require.Len(t, spans, 1)
				if attack {
					var blocked *events.BlockingSecurityEvent
					require.ErrorAs(t, monitorErr, &blocked)
					require.Equal(t, http.StatusTeapot, fctx.Response.StatusCode())
					require.NotContains(t, string(fctx.Response.Body()), "handler response")
					require.Equal(t, "418", spans[0].Tag(ext.HTTPCode))
					require.Contains(t, spans[0].Tag("_dd.appsec.json"), monitor.address)
				} else {
					require.NoError(t, monitorErr)
					require.Equal(t, http.StatusOK, fctx.Response.StatusCode())
					require.Equal(t, "handler response", string(fctx.Response.Body()))
					require.Nil(t, spans[0].Tag("_dd.appsec.json"))
				}
				_, found := dyngo.FromContext(&fctx)
				require.False(t, found, "the finished operation must not remain in the request context")
			})
		}
	}
}

func TestAppSecResponseBlockingStatus(t *testing.T) {
	startAppSecRegressionRules(t)

	for _, tc := range []struct {
		name       string
		status     int
		header     string
		wantStatus int
		wantError  bool
		opts       []Option
	}{
		{"clean", http.StatusOK, "clean", http.StatusOK, false, nil},
		{"header", http.StatusOK, "attack", http.StatusTeapot, false, nil},
		{"status", http.StatusInternalServerError, "clean", http.StatusTeapot, false, nil},
		{"custom-error", http.StatusOK, "attack", http.StatusTeapot, true, []Option{
			WithStatusCheck(func(status int) bool { return status == http.StatusTeapot }),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			var req fasthttp.Request
			req.SetRequestURI("http://example.test/")
			var fctx fasthttp.RequestCtx
			fctx.Init(&req, &net.TCPAddr{}, nil)

			WrapHandler(func(fctx *fasthttp.RequestCtx) {
				fctx.SetStatusCode(tc.status)
				fctx.Response.Header.Set("X-Block", tc.header)
				fctx.SetBodyString("handler response")
			}, tc.opts...)(&fctx)

			require.Equal(t, tc.wantStatus, fctx.Response.StatusCode())
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			require.Equal(t, strconv.Itoa(tc.wantStatus), spans[0].Tag(ext.HTTPCode))
			if tc.wantError {
				require.Equal(t, "418: I'm a teapot", spans[0].Tag(ext.ErrorMsg))
			} else {
				require.Nil(t, spans[0].Tag(ext.ErrorMsg))
			}
			if tc.wantStatus == http.StatusTeapot {
				require.NotContains(t, string(fctx.Response.Body()), "handler response")
				require.Empty(t, fctx.Response.Header.Peek("X-Block"))
				require.Contains(t, spans[0].Tag("_dd.appsec.json"), "fasthttp-body-response")
			} else {
				require.Equal(t, "handler response", string(fctx.Response.Body()))
				require.Nil(t, spans[0].Tag("_dd.appsec.json"))
			}
		})
	}
}

func TestAppSecContextRestoration(t *testing.T) {
	startAppSecRegressionRules(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var req fasthttp.Request
	req.SetRequestURI("http://example.test/")
	var fctx fasthttp.RequestCtx
	fctx.Init(&req, &net.TCPAddr{}, nil)
	fctx.SetUserValue("application-value", "preserved")

	var previous *httpsec.HandlerOperation
	handler := WrapHandler(func(fctx *fasthttp.RequestCtx) {
		outer, found := dyngo.FindOperation[httpsec.HandlerOperation](fctx)
		require.True(t, found)
		require.NotSame(t, previous, outer)
		previous = outer
		WrapHandler(func(fctx *fasthttp.RequestCtx) {
			inner, found := dyngo.FindOperation[httpsec.HandlerOperation](fctx)
			require.True(t, found)
			require.NotSame(t, outer, inner)
		})(fctx)
		restored, found := dyngo.FindOperation[httpsec.HandlerOperation](fctx)
		require.True(t, found)
		require.Same(t, outer, restored)
	})
	for range 2 {
		handler(&fctx)
		_, found := dyngo.FromContext(&fctx)
		require.False(t, found)
		require.Equal(t, "preserved", fctx.UserValue("application-value"))
	}
	require.Len(t, mt.FinishedSpans(), 4)
}

func TestAppSecEarlyBlockContextCleanup(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/blocking.json")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
	testutils.StartAppSec(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var req fasthttp.Request
	req.SetRequestURI("http://example.test/")
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	var fctx fasthttp.RequestCtx
	fctx.Init(&req, &net.TCPAddr{}, nil)
	WrapHandler(func(*fasthttp.RequestCtx) {
		t.Fatal("an early block must skip the handler")
	})(&fctx)
	require.Equal(t, http.StatusForbidden, fctx.Response.StatusCode())
	_, found := dyngo.FromContext(&fctx)
	require.False(t, found)
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	require.Equal(t, "403", spans[0].Tag(ext.HTTPCode))
}

func TestAppSecHandlerPanic(t *testing.T) {
	startAppSecRegressionRules(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var req fasthttp.Request
	req.SetRequestURI("http://example.test/")
	var fctx fasthttp.RequestCtx
	fctx.Init(&req, &net.TCPAddr{}, nil)

	finished := 0
	handler := WrapHandler(func(fctx *fasthttp.RequestCtx) {
		op, found := dyngo.FindOperation[httpsec.HandlerOperation](fctx)
		require.True(t, found)
		dyngo.OnFinish(op, func(*httpsec.HandlerOperation, httpsec.HandlerOperationRes) {
			finished++
		})
		fctx.Response.Header.Set("X-Block", "attack")
		panic("handler panic")
	})
	require.PanicsWithValue(t, "handler panic", func() { handler(&fctx) })
	require.Equal(t, 1, finished)
	_, found := dyngo.FromContext(&fctx)
	require.False(t, found)
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	require.Contains(t, spans[0].Tag("_dd.appsec.json"), "fasthttp-body-response")
	require.Equal(t, "418", spans[0].Tag(ext.HTTPCode))
}
