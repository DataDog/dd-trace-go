// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gearbox

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gogearbox/gearbox"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test that the gearbox request context retains the tracer context
// func TestChildSpan(t *testing.T) {
// 	assert := assert.New(t)
// 	mt := mocktracer.Start()
// 	defer mt.Stop()

// 	go newGbServer()

// 	client := &http.Client{
// 		Timeout: 3 * time.Second,
// 	}
// 	_, err := client.Get("http://127.0.0.1:1234/any")
// 	require.Equal(t, nil, err)

// 	_, ok := tracer.SpanFromContext(reqctx)
// 	assert.True(ok)
// }

// func TestPropagation(t *testing.T) {
// 	t.Run("inject-extract", func(t *testing.T) {
// 		assert := assert.New(t)
// 		mt := mocktracer.Start()
// 		defer mt.Stop()

// 		fcc := &gearboxutil.FastHTTPHeadersCarrier{}

// 		go newGbServer()

// 		client := &http.Client{
// 			Timeout: 3 * time.Second,
// 		}
// 		_, err := client.Get("http://127.0.0.1:1234/any")
// 		require.Equal(t, nil, err)
// 		spans := mt.FinishedSpans()

// 		gb := &GearboxContextMock{requestCtx: reqctx}

// 		pspan := tracer.StartSpan("test")
// 		err := tracer.Inject(pspan.Context(), fcc)
// 		if err != nil {
// 			t.Fatalf("Trace injection failed")
// 		}
// 		Middleware(WithServiceName("gb"))(gb)
// 		sctx, err := tracer.Extract(fcc)
// 		if err != nil {
// 			t.Fatalf("Trace extraction failed")
// 		}
// 		assert.Equal(sctx.TraceID(), pspan.Context().TraceID())
// 		assert.Equal(sctx.SpanID(), pspan.Context().SpanID())
// 	})
// 	t.Run("req-context", func(t *testing.T) {
// 		assert := assert.New(t)
// 		mt := mocktracer.Start()
// 		defer mt.Stop()

// 		reqctx := newReqCtx(200)
// 		gb := &GearboxContextMock{requestCtx: reqctx}
// 		fcc := &gearboxutil.FastHTTPHeadersCarrier{ReqHeader: &gb.Context().Request.Header}

// 		pspan := tracer.StartSpan("test")
// 		err := tracer.Inject(pspan.Context(), fcc)
// 		if err != nil {
// 			t.Fatalf("Trace injection failed")
// 		}
// 		Middleware(WithServiceName("gb"))(gb)
// 		span, ok := tracer.SpanFromContext(gb.Context())
// 		assert.True(ok)
// 		assert.Equal(span.(mocktracer.Span).TraceID(), pspan.(mocktracer.Span).TraceID())
// 		assert.Equal(span.(mocktracer.Span).ParentID(), pspan.(mocktracer.Span).SpanID())
// 	})
// }

var errMsg = "This is an error!"

func customErrChecker(statusCode int) bool {
	return statusCode >= 600
}

var customRsc = "custom resource"

func resourceNamer(gctx gearbox.Context) string {
	return customRsc
}
func ignoreResources(c gearbox.Context) bool {
	return strings.HasPrefix(string(c.Context().URI().Path()), "/any")
}

func newGbServer(opts ...Option) {
	gb := gearbox.New()
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
	gb.Start(":1234")
}

// Test all of the expected span metadata on a "default" span
func TestTrace200(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	go newGbServer()

	client := &http.Client{
		Timeout: 3 * time.Second,
	}
	_, err := client.Get("http://127.0.0.1:1234/any")
	require.Equal(t, nil, err)
	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal("GET /any", span.Tag(ext.ResourceName))
	assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
	assert.Equal("gearbox", span.Tag(ext.ServiceName))
	assert.Equal("200", span.Tag(ext.HTTPCode))
	assert.Equal("GET", span.Tag(ext.HTTPMethod))
	assert.Equal("http://127.0.0.1:1234/any", span.Tag(ext.HTTPURL))
	assert.Equal("gogearbox/gearbox.v1", span.Tag(ext.Component))
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
}

// Test that HTTP Status codes >= 500 get treated as error spans
func TestStatusError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	go newGbServer()

	client := &http.Client{
		Timeout: 3 * time.Second,
	}
	_, err := client.Get("http://127.0.0.1:1234/err")
	require.Equal(t, nil, err)
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
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		go newGbServer(WithStatusCheck(customErrChecker))

		client := &http.Client{
			Timeout: 3 * time.Second,
		}
		_, err := client.Get("http://127.0.0.1:1234/customErr")
		require.Equal(t, nil, err)
		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]
		assert.Equal("600", span.Tag(ext.HTTPCode))
		require.Contains(t, span.Tags(), ext.Error)
		wantErr := fmt.Sprintf("%d: %s", 600, errMsg)
		assert.Equal(wantErr, span.Tag(ext.Error).(error).Error())
	})
	t.Run("notError", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		go newGbServer(WithStatusCheck(customErrChecker))

		client := &http.Client{
			Timeout: 3 * time.Second,
		}
		_, err := client.Get("http://127.0.0.1:1234/err")
		require.Equal(t, nil, err)
		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]
		assert.Equal("500", span.Tag(ext.HTTPCode))
		assert.NotContains(span.Tags(), ext.Error)
	})
}

// Test that users can customize how resource_name is determined
func TestCustomResourceNamer(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	go newGbServer(WithResourceNamer(resourceNamer))

	client := &http.Client{
		Timeout: 3 * time.Second,
	}
	_, err := client.Get("http://127.0.0.1:1234/any")
	require.Equal(t, nil, err)
	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal(customRsc, span.Tag(ext.ResourceName))
}

// Test that the trace middleware passes the context off to the next handler in the req chain even if the request is not instrumented
func TestWithIgnoreRequest(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	go newGbServer(WithIgnoreRequest(ignoreResources))

	client := &http.Client{
		Timeout: 3 * time.Second,
	}
	resp, err := client.Get("http://127.0.0.1:1234/any")
	require.Equal(t, nil, err)
	assert.Len(mt.FinishedSpans(), 0)
	assert.Equal(200, resp.StatusCode)
}

// Should I still call `go newGbServer()` for benchmarks?
func BenchmarkGearboxMiddleware(b *testing.B) {
	mt := mocktracer.Start()
	defer mt.Stop()

	for i := 0; i < b.N; i++ {
		go newGbServer()
	}
}

func BenchmarkGearboxMiddlewareWithOptions(b *testing.B) {
	mt := mocktracer.Start()
	defer mt.Stop()

	for i := 0; i < b.N; i++ {
		go newGbServer(WithServiceName("gb"), WithStatusCheck(customErrChecker), WithResourceNamer(resourceNamer), WithIgnoreRequest(ignoreResources))
	}
}

// BenchmarkGearbox is intended to serve as a comparison between gearbox with trace middleware v other middleware.
func BenchmarkGearbox(b *testing.B) {
	gb := gearbox.New()
	logMiddleware := func(ctx gearbox.Context) {
		fmt.Println("log message!")
		ctx.Next()
	}
	gb.Use(logMiddleware)
}
