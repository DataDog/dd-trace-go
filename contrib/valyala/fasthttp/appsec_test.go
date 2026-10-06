// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func startAppSecServer(t *testing.T, opts ...Option) string {
	handler := WrapHandler(func(fctx *fasthttp.RequestCtx) {
		fctx.SetStatusCode(http.StatusOK)
		fmt.Fprintf(fctx, "Hello World!\n")
	}, opts...)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &fasthttp.Server{Handler: handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		require.NoError(t, srv.Shutdown())
	})
	return "http://" + ln.Addr().String()
}

func TestAppSec(t *testing.T) {
	t.Setenv("DD_APPSEC_ENABLED", "true")
	testutils.StartAppSec(t)

	url := startAppSecServer(t)
	client := &http.Client{Timeout: 10 * time.Second}

	// An LFI attack in the request URI (appsec rule crs-930-110).
	t.Run("request-uri", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest(http.MethodGet, url+"/etc/passwd", nil)
		require.NoError(t, err)
		// Sent raw so the path traversal reaches the server unresolved.
		req.URL.Opaque = "//" + req.URL.Host + "/../../../secret.txt"
		res, err := client.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		event, ok := spans[0].Tag("_dd.appsec.json").(string)
		require.True(t, ok, "expected an appsec event on the request span")
		require.Contains(t, event, "server.request.uri.raw")
		require.Contains(t, event, "crs-930-110")
	})

	// A security scanner attack in a request header (appsec rule crs-913-120).
	t.Run("request-headers", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest(http.MethodGet, url+"/", nil)
		require.NoError(t, err)
		req.Header.Set("User-Agent", "Arachni/v1")
		res, err := client.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		event, ok := spans[0].Tag("_dd.appsec.json").(string)
		require.True(t, ok, "expected an appsec event on the request span")
		require.Contains(t, event, "ua0-600-12x")
	})

	// A clean request must still reach the handler and carry no event.
	t.Run("no-event", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		res, err := client.Get(url + "/")
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello World!\n", string(body))

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		require.Nil(t, spans[0].Tag("_dd.appsec.json"))
	})
}

func TestAppSecBlocking(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/blocking.json")
	testutils.StartAppSec(t)

	url := startAppSecServer(t)
	client := &http.Client{Timeout: 10 * time.Second}

	// The blocking ruleset blocks on the client IP, which is known before the
	// handler runs, so the handler must never be reached.
	t.Run("block", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest(http.MethodGet, url+"/", nil)
		require.NoError(t, err)
		req.Header.Set("X-Forwarded-For", "1.2.3.4")
		res, err := client.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()

		require.Equal(t, http.StatusForbidden, res.StatusCode)
		require.Equal(t, "application/json", res.Header.Get("Content-Type"))
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NotContains(t, string(body), "Hello World!")

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		require.Equal(t, "403", spans[0].Tag("http.status_code"))
		require.NotNil(t, spans[0].Tag("_dd.appsec.json"))
	})

	t.Run("block-html", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest(http.MethodGet, url+"/", nil)
		require.NoError(t, err)
		req.Header.Set("X-Forwarded-For", "1.2.3.4")
		req.Header.Set("Accept", "text/html")
		res, err := client.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()

		require.Equal(t, http.StatusForbidden, res.StatusCode)
		require.Equal(t, "text/html", res.Header.Get("Content-Type"))
	})

	t.Run("no-block", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		res, err := client.Get(url + "/")
		require.NoError(t, err)
		defer res.Body.Close()

		require.Equal(t, http.StatusOK, res.StatusCode)
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello World!\n", string(body))
	})
}

// startAppSecRules starts AppSec with the given WAF ruleset.
func startAppSecRules(t *testing.T, rules string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.json")
	require.NoError(t, os.WriteFile(path, []byte(rules), 0o600))
	t.Setenv("DD_APPSEC_RULES", path)
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
	testutils.StartAppSec(t)
}

// appSecRegressionRules blocks with status 418 when a request or response
// body, the X-Block response header, or the response status matches.
const appSecRegressionRules = `{
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

func startAppSecRegressionRules(t *testing.T) {
	t.Helper()
	startAppSecRules(t, appSecRegressionRules)
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
	// The handler panicked, so the response status is not the result.
	require.Nil(t, spans[0].Tag(ext.HTTPCode))
	require.Equal(t, errHandlerPanic.Error(), spans[0].Tag(ext.ErrorMsg))
}

// TestAppSecRequestTargets sends raw requests that fasthttp serves but that
// net/http parses differently: url.ParseRequestURI rejects the target, or
// url.ParseQuery or http.Request.Cookies drop values. AppSec must inspect what
// the handler sees.
func TestAppSecRequestTargets(t *testing.T) {
	rules := `{
		"version": "2.2",
		"metadata": {"rules_version": "1.0.0"},
		"rules": [{
			"id": "fasthttp-client-ip",
			"name": "Block client IP",
			"tags": {"type": "test", "category": "attack_attempt"},
			"conditions": [{
				"operator": "ip_match",
				"parameters": {"inputs": [{"address": "http.client_ip"}], "list": ["1.2.3.4"]}
			}],
			"on_match": ["block"]
		}, {
			"id": "fasthttp-query-cookie",
			"name": "Block query or cookie input",
			"tags": {"type": "test", "category": "attack_attempt"},
			"conditions": [{
				"operator": "phrase_match",
				"parameters": {
					"inputs": [{"address": "server.request.query"}, {"address": "server.request.cookies"}],
					"list": ["$globals"]
				}
			}],
			"on_match": ["block"]
		}]
	}`
	startAppSecRules(t, rules)

	manyCookies := strings.Repeat("c=1; ", 3000) + "attack=$globals"
	for _, tc := range []struct {
		name    string
		target  string
		headers string
		rule    string
	}{
		{"invalid-path-escape/client-ip", "/%GG", "X-Forwarded-For: 1.2.3.4\r\n", "fasthttp-client-ip"},
		{"invalid-path-escape/query", "/%GG?x=$globals", "", "fasthttp-query-cookie"},
		{"invalid-query-escape", "/?x=%GG$globals", "", "fasthttp-query-cookie"},
		{"query-semicolon", "/?a=1;x=$globals", "", "fasthttp-query-cookie"},
		{"cookie-backslash", "/", "Cookie: attack=$globals\\\r\n", "fasthttp-query-cookie"},
		{"cookie-count", "/", "Cookie: " + manyCookies + "\r\n", "fasthttp-query-cookie"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			handlerCalled := false
			handler := WrapHandler(func(fctx *fasthttp.RequestCtx) {
				handlerCalled = true
				fctx.SetBodyString("handler response")
			})
			conns := fasthttputil.NewPipeConns()
			t.Cleanup(func() { _ = conns.Close() })
			deadline := time.Now().Add(10 * time.Second)
			require.NoError(t, conns.Conn1().SetDeadline(deadline))
			require.NoError(t, conns.Conn2().SetDeadline(deadline))
			served := make(chan error, 1)
			// The cookie-count case needs a buffer larger than the default.
			srv := &fasthttp.Server{Handler: handler, ReadBufferSize: 64 << 10}
			go func() { served <- srv.ServeConn(conns.Conn1()) }()

			client := conns.Conn2()
			_, err := io.WriteString(client, "GET "+tc.target+" HTTP/1.1\r\nHost: example.test\r\nConnection: close\r\n"+tc.headers+"\r\n")
			require.NoError(t, err)
			var res fasthttp.Response
			require.NoError(t, res.Read(bufio.NewReader(client)))
			require.NoError(t, client.Close())
			require.NoError(t, <-served)

			require.False(t, handlerCalled, "a blocked request must not reach the handler")
			require.Equal(t, http.StatusForbidden, res.StatusCode())
			require.NotContains(t, string(res.Body()), "handler response")
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			require.Equal(t, "403", spans[0].Tag(ext.HTTPCode))
			require.Equal(t, "true", spans[0].Tag("appsec.blocked"))
			require.Contains(t, spans[0].Tag("_dd.appsec.json"), tc.rule)
		})
	}
}

// TestAppSecArgsFallback covers the case where fasthttp finds no cookie or
// query pairs. collectArgs then returns nil, and httpsec parses the converted
// request with net/http. The WAF must see the values that net/http finds.
//
// No request that we found makes fasthttp drop a pair that net/http keeps, so
// this test gives the converted request to httpsec without the fasthttp pairs.
func TestAppSecArgsFallback(t *testing.T) {
	startAppSecRules(t, `{
		"version": "2.2",
		"metadata": {"rules_version": "1.0.0"},
		"rules": [{
			"id": "fasthttp-query-cookie",
			"name": "Block query or cookie input",
			"tags": {"type": "test", "category": "attack_attempt"},
			"conditions": [{
				"operator": "phrase_match",
				"parameters": {
					"inputs": [{"address": "server.request.query"}, {"address": "server.request.cookies"}],
					"list": ["$globals"]
				}
			}],
			"on_match": ["block"]
		}]
	}`)
	noPairs := func(func([]byte, []byte) bool) {}

	for _, tc := range []struct {
		name   string
		target string
		cookie string
	}{
		{"query", "http://example.test/?x=$globals", ""},
		{"cookie", "http://example.test/", "attack=$globals"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			var req fasthttp.Request
			req.SetRequestURI(tc.target)
			if tc.cookie != "" {
				req.Header.Set("Cookie", tc.cookie)
			}
			var fctx fasthttp.RequestCtx
			fctx.Init(&req, &net.TCPAddr{}, nil)

			span := mt.StartSpan("http.request")
			w := &responseWriter{ctx: &fctx, response: &fctx.Response}
			_, _, finish, handled := httpsec.BeforeHandle(w, convertRequest(&fctx), span, &httpsec.Config{
				Framework:            appsecFramework,
				Cookies:              collectArgs(noPairs),
				QueryParams:          collectArgs(noPairs),
				ResponseHeaderCopier: func(http.ResponseWriter) http.Header { return responseHeaders(w.response) },
			})
			finish()
			span.Finish()

			require.True(t, handled, "the WAF must see the value that net/http finds")
			require.Equal(t, http.StatusForbidden, fctx.Response.StatusCode())
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			require.Contains(t, spans[0].Tag("_dd.appsec.json"), "$globals")
		})
	}
}

// TestAppSecSpanOutlivesConnectionBuffer serves two requests on one
// connection. fasthttp reuses its request storage for the second request.
// The span of the first request must keep the values of the first request:
// convertRequest copies them.
func TestAppSecSpanOutlivesConnectionBuffer(t *testing.T) {
	// The rule only monitors, so that the connection stays open.
	startAppSecRules(t, `{
		"version": "2.2",
		"metadata": {"rules_version": "1.0.0"},
		"rules": [{
			"id": "fasthttp-user-agent",
			"name": "Monitor user agent",
			"tags": {"type": "test", "category": "attack_attempt"},
			"conditions": [{
				"operator": "phrase_match",
				"parameters": {
					"inputs": [{"address": "server.request.headers.no_cookies", "key_path": ["user-agent"]}],
					"list": ["dd-attack-ua"]
				}
			}]
		}]
	}`)
	mt := mocktracer.Start()
	defer mt.Stop()

	conns := fasthttputil.NewPipeConns()
	t.Cleanup(func() { _ = conns.Close() })
	deadline := time.Now().Add(10 * time.Second)
	require.NoError(t, conns.Conn1().SetDeadline(deadline))
	require.NoError(t, conns.Conn2().SetDeadline(deadline))
	served := make(chan error, 1)
	srv := &fasthttp.Server{Handler: WrapHandler(func(fctx *fasthttp.RequestCtx) {
		fctx.SetBodyString("ok")
	})}
	go func() { served <- srv.ServeConn(conns.Conn1()) }()

	client := conns.Conn2()
	reader := bufio.NewReader(client)
	// Both requests have the same layout, so that the second request writes
	// its values at the same place as the first.
	for _, raw := range []string{
		"GET /attack-path HTTP/1.1\r\nHost: example.test\r\nUser-Agent: dd-attack-ua\r\n\r\n",
		"GET /zzzzzz-zzzz HTTP/1.1\r\nHost: example.test\r\nUser-Agent: zz-zzzzzz-zz\r\nConnection: close\r\n\r\n",
	} {
		_, err := io.WriteString(client, raw)
		require.NoError(t, err)
		var res fasthttp.Response
		require.NoError(t, res.Read(reader))
		require.Equal(t, http.StatusOK, res.StatusCode())
	}
	require.NoError(t, client.Close())
	require.NoError(t, <-served)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)
	first := spans[0]
	require.Equal(t, "dd-attack-ua", first.Tag("http.request.headers.user-agent"))
	require.Contains(t, first.Tag("_dd.appsec.json"), "dd-attack-ua")
	require.Equal(t, "zz-zzzzzz-zz", spans[1].Tag("http.request.headers.user-agent"))
	require.Nil(t, spans[1].Tag("_dd.appsec.json"))
}

// appSecDoubleBlockRules blocks a request with the X-Attack: attack header.
// A second rule blocks a response with status 403, which is the status of the
// first block. The rules have different types: with one type, the WAF did not
// report the second match.
const appSecDoubleBlockRules = `{
	"version": "2.2",
	"metadata": {"rules_version": "1.0.0"},
	"rules": [{
		"id": "fasthttp-request-header",
		"name": "Block request header",
		"tags": {"type": "test-request", "category": "attack_attempt"},
		"conditions": [{
			"operator": "exact_match",
			"parameters": {
				"inputs": [{"address": "server.request.headers.no_cookies", "key_path": ["x-attack"]}],
				"list": ["attack"]
			}
		}],
		"on_match": ["block"]
	}, {
		"id": "fasthttp-block-status",
		"name": "Block the block status",
		"tags": {"type": "test-response", "category": "attack_attempt"},
		"conditions": [{
			"operator": "exact_match",
			"parameters": {"inputs": [{"address": "server.response.status"}], "list": ["403"]}
		}],
		"on_match": ["block"]
	}]
}`

// TestAppSecSecondBlockKeepsOneBody blocks a request early. Then a response
// rule matches the status of the block response and blocks again. The client
// must get one block response body, not two.
func TestAppSecSecondBlockKeepsOneBody(t *testing.T) {
	startAppSecRules(t, appSecDoubleBlockRules)
	mt := mocktracer.Start()
	defer mt.Stop()
	var req fasthttp.Request
	req.SetRequestURI("http://example.test/")
	req.Header.Set("X-Attack", "attack")
	var fctx fasthttp.RequestCtx
	fctx.Init(&req, &net.TCPAddr{}, nil)

	handlerCalled := false
	WrapHandler(func(*fasthttp.RequestCtx) { handlerCalled = true })(&fctx)

	require.False(t, handlerCalled)
	require.Equal(t, http.StatusForbidden, fctx.Response.StatusCode())
	body := string(fctx.Response.Body())
	require.Equal(t, 1, strings.Count(body, `"errors"`), "the response must hold one block payload: %s", body)
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	appsecJSON, _ := spans[0].Tag("_dd.appsec.json").(string)
	require.Contains(t, appsecJSON, "fasthttp-request-header")
	require.Contains(t, appsecJSON, "fasthttp-block-status", "the second block must run")
}

// TestAppSecBlockAfterApplicationTimeout makes the handler call
// ctx.TimeoutErrorWithCode. fasthttp then sends the timeout response and
// discards the live response. A block from a response rule cannot reach the
// client, so AppSec must not report it as delivered.
func TestAppSecBlockAfterApplicationTimeout(t *testing.T) {
	startAppSecRegressionRules(t)
	recorder := testutils.StartTelemetryRecorder(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var req fasthttp.Request
	req.SetRequestURI("http://example.test/")
	var fctx fasthttp.RequestCtx
	fctx.Init(&req, &net.TCPAddr{}, nil)

	WrapHandler(func(fctx *fasthttp.RequestCtx) {
		fctx.Response.Header.Set("X-Block", "attack")
		fctx.TimeoutErrorWithCode("application timeout", http.StatusGatewayTimeout)
	})(&fctx)

	timeoutResponse := fctx.LastTimeoutErrorResponse()
	require.NotNil(t, timeoutResponse)
	require.Equal(t, http.StatusGatewayTimeout, timeoutResponse.StatusCode())
	require.Equal(t, "application timeout", string(timeoutResponse.Body()))
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	require.Contains(t, spans[0].Tag("_dd.appsec.json"), "fasthttp-body-response")
	require.Nil(t, spans[0].Tag("appsec.blocked"), "a block that does not reach the client is not delivered")
	requireBlockFailed(t, recordedMetrics(recorder.Metrics))
}

// recordedMetrics returns the name and the tags of each metric that has a
// value. The telemetry recorder type is internal to dd-trace-go/v2, so this
// function accepts its metrics map through type inference.
func recordedMetrics[K interface{ comparable }, H interface{ Get() float64 }](metrics map[K]H) []any {
	var keys []any
	for key, handle := range metrics {
		if handle.Get() > 0 {
			keys = append(keys, key)
		}
	}
	return keys
}

// requireBlockFailed checks that the waf.requests metric reports one request
// with a block that failed.
func requireBlockFailed(t *testing.T, keys []any) {
	t.Helper()
	var outcomes []string
	for _, key := range keys {
		v := reflect.ValueOf(key)
		if v.FieldByName("Name").String() != "waf.requests" {
			continue
		}
		for _, tag := range strings.Split(v.FieldByName("Tags").String(), ",") {
			if strings.HasPrefix(tag, "request_blocked:") || strings.HasPrefix(tag, "block_failure:") {
				outcomes = append(outcomes, tag)
			}
		}
	}
	require.ElementsMatch(t, []string{"request_blocked:false", "block_failure:true"}, outcomes,
		"the block must be reported as failed")
}

// TestAppSecBlockAfterEarlierTimeout calls ctx.TimeoutErrorWithCode before
// WrapHandler starts. fasthttp then sends that timeout response, so an early
// block cannot reach the client and must not be reported as delivered.
func TestAppSecBlockAfterEarlierTimeout(t *testing.T) {
	startAppSecRules(t, appSecDoubleBlockRules)
	recorder := testutils.StartTelemetryRecorder(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var req fasthttp.Request
	req.SetRequestURI("http://example.test/")
	req.Header.Set("X-Attack", "attack")
	var fctx fasthttp.RequestCtx
	fctx.Init(&req, &net.TCPAddr{}, nil)

	handlerCalled := false
	wrapped := WrapHandler(func(*fasthttp.RequestCtx) { handlerCalled = true })
	func(fctx *fasthttp.RequestCtx) {
		fctx.TimeoutErrorWithCode("application timeout", http.StatusGatewayTimeout)
		wrapped(fctx)
	}(&fctx)

	// The block still stops the handler.
	require.False(t, handlerCalled)
	require.Equal(t, http.StatusGatewayTimeout, fctx.LastTimeoutErrorResponse().StatusCode())
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	require.Contains(t, spans[0].Tag("_dd.appsec.json"), "fasthttp-request-header")
	require.Nil(t, spans[0].Tag("appsec.blocked"))
	requireBlockFailed(t, recordedMetrics(recorder.Metrics))
}
