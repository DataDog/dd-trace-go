// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttp

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	instrhttptrace "github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

const errMsg = "This is an error!"

func ignoreResources(fctx *fasthttp.RequestCtx) bool {
	return strings.HasPrefix(string(fctx.URI().Path()), "/any")
}

func startServer(t *testing.T, opts ...Option) string {
	return startServerWithConfig(t, nil, opts...)
}

func startServerWithConfig(t *testing.T, configure func(*fasthttp.Server), opts ...Option) string {
	router := WrapHandler(func(fctx *fasthttp.RequestCtx) {
		switch string(fctx.Path()) {
		case "/any":
			fmt.Fprintf(fctx, "Hi there!")
			return
		case "/err":
			fctx.Error(errMsg, 500)
			return
		case "/customErr":
			fctx.Error(errMsg, 600)
			return
		case "/contextExtract":
			_, ok := tracer.SpanFromContext(fctx)
			if !ok {
				fctx.Error("No span in the request context", 500)
				return
			}
			fctx.SetStatusCode(200)
			fmt.Fprintf(fctx, "Hi there! RequestURI is %q", fctx.RequestURI())
			return
		default:
			fctx.Error("not found", fasthttp.StatusNotFound)
			return
		}
	}, opts...)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := ln.Addr()
	server := &fasthttp.Server{
		Handler: router,
	}
	if configure != nil {
		configure(server)
	}
	go func() {
		require.NoError(t, server.Serve(ln))
	}()
	// Stop the server at the end of each test run
	t.Cleanup(func() {
		assert.NoError(t, server.Shutdown())
	})

	timeoutChan := time.After(5 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	httpAddr := "http://" + addr.String()
	checkServerReady := func() bool {
		resp, err := (&http.Client{}).Get(httpAddr + "/any")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == 200
	}
	// Keep checking if server is up. If not, wait 100ms or timeout.
	for {
		// If the server is up, return the address
		if checkServerReady() {
			return httpAddr
		}
		select {
		case <-timeoutChan:
			assert.FailNow(t, "Timed out waiting for FastHTTP server to start up")
		case <-ticker.C:
			continue
		}
	}
}

// Test all of the expected span metadata on a "default" span
func TestTrace200(t *testing.T) {
	addr := startServer(t)

	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	resp, err := (&http.Client{}).Get(addr + "/any")
	require.NoError(t, err)
	defer resp.Body.Close()

	spans := mt.FinishedSpans()

	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal("GET /any", span.Tag(ext.ResourceName))
	assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
	assert.Equal("fasthttp", span.Tag(ext.ServiceName))
	assert.Equal("200", span.Tag(ext.HTTPCode))
	assert.Equal("GET", span.Tag(ext.HTTPMethod))
	assert.Equal(addr+"/any", span.Tag(ext.HTTPURL))
	assert.Equal(string(instrumentation.PackageValyalaFastHTTP), span.Tag(ext.Component))
	assert.Equal(string(instrumentation.PackageValyalaFastHTTP), span.Integration())
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
}

// Test that the http.url span tag redacts sensitive query string parameters instead of
// leaking them verbatim (APMSP-3529).
func TestHTTPURLQueryStringObfuscation(t *testing.T) {
	addr := startServer(t)
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	resp, err := (&http.Client{}).Get(addr + "/any?token=supersecret&safe=1")
	require.NoError(t, err)
	defer resp.Body.Close()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	url, _ := spans[0].Tag(ext.HTTPURL).(string)
	assert.Contains(url, "safe=1")
	assert.Contains(url, "<redacted>")
	assert.NotContains(url, "supersecret")
}

func TestHTTPURLQueryStringDisabled(t *testing.T) {
	t.Cleanup(instrhttptrace.ResetCfg)
	t.Setenv("DD_TRACE_HTTP_URL_QUERY_STRING_DISABLED", "true")
	instrhttptrace.ResetCfg()

	addr := startServer(t)
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	resp, err := (&http.Client{}).Get(addr + "/any?token=supersecret")
	require.NoError(t, err)
	defer resp.Body.Close()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(addr+"/any", spans[0].Tag(ext.HTTPURL))
}

func TestHTTPURLQueryStringCustomRegexp(t *testing.T) {
	t.Cleanup(instrhttptrace.ResetCfg)
	t.Setenv("DD_TRACE_OBFUSCATION_QUERY_STRING_REGEXP", `myparam=\w+`)
	instrhttptrace.ResetCfg()

	addr := startServer(t)
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	resp, err := (&http.Client{}).Get(addr + "/any?myparam=shouldberedacted&other=1")
	require.NoError(t, err)
	defer resp.Body.Close()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	url, _ := spans[0].Tag(ext.HTTPURL).(string)
	assert.Contains(url, "other=1")
	assert.Contains(url, "<redacted>")
	assert.NotContains(url, "shouldberedacted")
}

func TestHTTPURLQueryStringAllowlist(t *testing.T) {
	t.Cleanup(instrhttptrace.ResetCfg)
	t.Setenv("DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST_SERVER", "safe")
	instrhttptrace.ResetCfg()

	addr := startServer(t)
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	resp, err := (&http.Client{}).Get(addr + "/any?safe=1&password=hunter2")
	require.NoError(t, err)
	defer resp.Body.Close()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	url, _ := spans[0].Tag(ext.HTTPURL).(string)
	assert.Contains(url, "safe=1")
	assert.NotContains(url, "hunter2")
	assert.NotContains(url, "password")
}

// Test that the http.url span tag preserves the raw, as-received wire-form path
// (no dot-segment collapsing, no %2F decoding) rather than a normalized one. A
// regular net/http.Client would normalize a path like "/a/b/../c" before it ever
// reaches the wire, so this dials the server directly and writes the request
// line by hand to control the exact bytes sent.
func TestHTTPURLPreservesRawPath(t *testing.T) {
	addr := startServer(t)
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	rawAddr := strings.TrimPrefix(addr, "http://")
	conn, err := net.Dial("tcp", rawAddr)
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.Write([]byte("GET /a/b/../c HTTP/1.1\r\nHost: " + rawAddr + "\r\nConnection: close\r\n\r\n"))
	require.NoError(t, err)
	_, _ = io.ReadAll(conn) // drain the response so the span finishes before we inspect it

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(addr+"/a/b/../c", spans[0].Tag(ext.HTTPURL))
}

// Test that HTTP Status codes >= 500 are treated as error spans
func TestStatusError(t *testing.T) {
	addr := startServer(t)

	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	resp, err := (&http.Client{}).Get(addr + "/err")
	require.NoError(t, err)
	defer resp.Body.Close()

	spans := mt.FinishedSpans()

	require.Len(t, spans, 1)
	span := spans[0]
	assert.Equal("500", span.Tag(ext.HTTPCode))
	wantErr := fmt.Sprintf("%d: %s", 500, errMsg)
	assert.Equal(wantErr, span.Tag(ext.ErrorMsg))
}

// Test that users can customize which HTTP status codes are considered an error
func TestWithStatusCheck(t *testing.T) {
	customErrChecker := func(statusCode int) bool {
		return statusCode >= 600
	}
	t.Run("isError", func(t *testing.T) {
		addr := startServer(t, WithStatusCheck(customErrChecker))

		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()
		c := &http.Client{}
		resp, err := c.Get(addr + "/customErr")
		require.NoError(t, err)
		defer resp.Body.Close()

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		span := spans[0]
		assert.Equal("600", span.Tag(ext.HTTPCode))
		require.Contains(t, span.Tags(), ext.ErrorMsg)
		wantErr := fmt.Sprintf("%d: %s", 600, errMsg)
		assert.Equal(wantErr, span.Tag(ext.ErrorMsg))
	})
	t.Run("notError", func(t *testing.T) {
		addr := startServer(t, WithStatusCheck(customErrChecker))

		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		resp, err := (&http.Client{}).Get(addr + "/err")
		require.NoError(t, err)
		defer resp.Body.Close()

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		span := spans[0]
		assert.Equal("500", span.Tag(ext.HTTPCode))
		assert.NotContains(span.Tags(), ext.ErrorMsg)
	})
}

// Test that users can customize how resource_name is determined
func TestCustomResourceNamer(t *testing.T) {
	customResourceNamer := func(_ *fasthttp.RequestCtx) string {
		return "custom resource"
	}
	addr := startServer(t, WithResourceNamer(customResourceNamer))

	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	resp, err := (&http.Client{}).Get(addr + "/any")
	require.NoError(t, err)
	defer resp.Body.Close()

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("custom resource", span.Tag(ext.ResourceName))
}

// Test that the trace middleware passes the context off to the next handler in the req chain even if the request is not instrumented
func TestWithIgnoreRequest(t *testing.T) {
	addr := startServer(t, WithIgnoreRequest(ignoreResources))

	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	resp, err := (&http.Client{}).Get(addr + "/any")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Len(mt.FinishedSpans(), 0)
	assert.Equal(200, resp.StatusCode)
}

// Test that tracer context is stored in fasthttp request context
func TestChildSpan(t *testing.T) {
	addr := startServer(t)

	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	resp, err := (&http.Client{}).Get(addr + "/contextExtract")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(200, resp.StatusCode)
}

// Test that distributed tracing works from client to fasthttp server
func TestPropagation(t *testing.T) {
	addr := startServer(t)

	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	c := httptrace.WrapClient(&http.Client{})
	resp, err := c.Get(addr + "/any")
	require.NoError(t, err)
	defer resp.Body.Close()

	spans := mt.FinishedSpans()
	require.Equal(t, 2, len(spans))
	one := spans[0]
	two := spans[1]
	assert.Equal(one.TraceID(), two.TraceID())
}

func TestSecurityTestingHeaders(t *testing.T) {
	assert := assert.New(t)
	addr := startServer(t)

	mt := mocktracer.Start()
	defer mt.Stop()

	req, err := http.NewRequest("GET", addr+"/any", nil)
	require.NoError(t, err)
	req.Header.Set("x-datadog-endpoint-scan", "true")
	req.Header.Set("x-datadog-security-test", "test-value")

	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)

	span := spans[0]
	assert.Equal("true", span.Tag(ext.HTTPRequestHeaders+".x-datadog-endpoint-scan"))
	assert.Equal("test-value", span.Tag(ext.HTTPRequestHeaders+".x-datadog-security-test"))
}

func TestSecurityTestingHeadersWithDisabledHeaderNamesNormalizing(t *testing.T) {
	assert := assert.New(t)
	addr := startServerWithConfig(t, func(server *fasthttp.Server) {
		server.DisableHeaderNamesNormalizing = true
	})

	mt := mocktracer.Start()
	defer mt.Stop()

	req, err := http.NewRequest("GET", addr+"/any", nil)
	require.NoError(t, err)
	req.Header.Set("X-Datadog-Endpoint-Scan", "true")
	req.Header.Set("X-Datadog-Security-Test", "test-value")

	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)

	span := spans[0]
	assert.Equal("true", span.Tag(ext.HTTPRequestHeaders+".x-datadog-endpoint-scan"))
	assert.Equal("test-value", span.Tag(ext.HTTPRequestHeaders+".x-datadog-security-test"))
}
