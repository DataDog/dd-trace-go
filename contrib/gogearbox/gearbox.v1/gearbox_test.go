// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gearbox

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/gogearbox/gearbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errMsg = "This is an error!"

func customErrChecker(statusCode int) bool {
	return statusCode >= 600
}

var customRsc = "custom resource"

func resourceNamer(_ gearbox.Context) string {
	return customRsc
}

func ignoreResources(c gearbox.Context) bool {
	return strings.HasPrefix(string(c.Context().URI().Path()), "/any")
}

func getFreePort(t *testing.T) int {
	li, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := li.Addr()
	err = li.Close()
	require.NoError(t, err)
	return addr.(*net.TCPAddr).Port
}

func startServer(t *testing.T, opts ...Option) string {
	// Setup gearbox
	gb := gearbox.New(&gearbox.Settings{
		DisableStartupMessage: true,
	})
	gb.Use(Middleware(opts...))
	gb.Get("/any", func(ctx gearbox.Context) {
		ctx.SendString("Hello World!")
	})
	gb.Get("/err", func(ctx gearbox.Context) {
		ctx.Context().Error(errMsg, 500)
	})
	gb.Get("/customErr", func(ctx gearbox.Context) {
		ctx.Context().Error(errMsg, 600)
	})
	gb.Get("/propagation", func(ctx gearbox.Context) {
		_, ok := tracer.SpanFromContext(ctx.Context())
		if !ok {
			ctx.Context().Error("No span in the request context", 500)
		} else {
			ctx.Status(200)
		}
	})
	addr := fmt.Sprintf("127.0.0.1:%d", getFreePort(t))

	// Start gearbox server in a separate goroutine
	go func() {
		require.NoError(t, gb.Start(addr))
	}()
	// Stop the server at the end of each test run
	t.Cleanup(func() {
		assert.NoError(t, gb.Stop())
	})

	timeoutChan := time.After(5 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	// Keep checking if server is up. If not, wait 100ms or timeout.
	for {
		httpAddr := "http://" + addr
		resp, err := http.DefaultClient.Get(httpAddr + "/any")
		assert.NoError(t, resp.Body.Close())

		// If the server is up, return the address
		if err == nil && resp.StatusCode == 200 {
			return httpAddr
		}
		select {
		case <-timeoutChan:
			assert.FailNow(t, "Timed out waiting for Gearbox server to start up")
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

	resp, err := http.DefaultClient.Get(addr + "/any")
	require.NoError(t, err)
	defer resp.Body.Close()

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal("GET /any", span.Tag(ext.ResourceName))
	assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
	assert.Equal("gearbox", span.Tag(ext.ServiceName))
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

	resp, err := http.DefaultClient.Get(addr + "/err")
	require.NoError(t, err)
	defer resp.Body.Close()

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal("gearbox", span.Tag(ext.ServiceName))
	assert.Equal("500", span.Tag(ext.HTTPCode))
	wantErr := fmt.Sprintf("%d: %s", 500, errMsg)
	assert.Equal(wantErr, span.Tag(ext.Error).(error).Error())
}

// Test that users can customize which HTTP status codes are considered an error
func TestWithStatusCheck(t *testing.T) {
	t.Run("isError", func(t *testing.T) {
		addr := startServer(t, WithStatusCheck(customErrChecker))

		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		resp, err := http.DefaultClient.Get(addr + "/customErr")
		require.NoError(t, err)
		defer resp.Body.Close()

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
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

		resp, err := http.DefaultClient.Get(addr + "/err")
		require.NoError(t, err)
		defer resp.Body.Close()

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]
		assert.Equal("500", span.Tag(ext.HTTPCode))
		assert.NotContains(span.Tags(), ext.Error)
	})
}

// Test that users can customize how resource_name is determined
func TestCustomResourceNamer(t *testing.T) {
	addr := startServer(t, WithResourceNamer(resourceNamer))

	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	resp, err := http.DefaultClient.Get(addr + "/any")
	require.NoError(t, err)
	defer resp.Body.Close()

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal(customRsc, span.Tag(ext.ResourceName))
}

// Test that the trace middleware passes the context off to the next handler in the req chain even if the request is not instrumented
func TestWithIgnoreRequest(t *testing.T) {
	addr := startServer(t, WithIgnoreRequest(ignoreResources))

	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	resp, err := http.DefaultClient.Get(addr + "/any")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Len(mt.FinishedSpans(), 0)
	assert.Equal(200, resp.StatusCode)
}

// Test that tracer context is stored in gearbox request context
func TestChildSpan(t *testing.T) {
	addr := startServer(t)

	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	resp, err := http.DefaultClient.Get(addr + "/propagation")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(200, resp.StatusCode)
}

// Test that distributed tracing works from client to gearbox server
func TestPropagation(t *testing.T) {
	addr := startServer(t)

	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	c := httptrace.WrapClient(http.DefaultClient)
	resp, err := c.Get(addr + "/any")
	require.NoError(t, err)
	defer resp.Body.Close()

	spans := mt.FinishedSpans()
	require.Equal(t, 2, len(spans))
	one := spans[0]
	two := spans[1]
	assert.Equal(one.TraceID(), two.TraceID())
}
