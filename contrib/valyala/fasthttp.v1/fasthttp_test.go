// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttp

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

const errMsg = "This is an error!"

func ignoreResources(fctx *fasthttp.RequestCtx) bool {
	return strings.HasPrefix(string(fctx.URI().Path()), "/any")
}

func startServer(t *testing.T, opts ...Option) string {
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
	assert.Equal(componentName, span.Tag(ext.Component))
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
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
	assert.Equal(wantErr, span.Tag(ext.Error).(error).Error())
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
		require.Contains(t, span.Tags(), ext.Error)
		wantErr := fmt.Sprintf("%d: %s", 600, errMsg)
		assert.Equal(wantErr, span.Tag(ext.Error).(error).Error())
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
		assert.NotContains(span.Tags(), ext.Error)
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
