// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gearbox

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/gogearbox/gearbox"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/gogearbox/gearbox.v1/internal/gearboxutil"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func newReqCtx(code int) *fasthttp.RequestCtx {
	reqctx := &fasthttp.RequestCtx{}
	reqctx.URI().Update("//foobar.com/any")
	reqctx.Request.Header.SetMethod("GET")
	reqctx.Response.SetStatusCode(code)
	return reqctx
}

// Test all of the expected span metadata on a "default" span
func TestTrace200(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	reqctx := newReqCtx(200)
	gb := &GearboxContextMock{requestCtx: reqctx}
	Middleware(WithServiceName("gb"))(gb)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)

	span := spans[0]
	fmt.Printf("\n%v", span)
	assert.Equal("http.request", span.OperationName())
	assert.Equal(span.Tag(ext.ResourceName), "GET /any")
	assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
	assert.Equal("gb", span.Tag(ext.ServiceName))
	assert.Equal("200", span.Tag(ext.HTTPCode))
	assert.Equal("GET", span.Tag(ext.HTTPMethod))
	assert.Equal("http://foobar.com/any", span.Tag(ext.HTTPURL))
	assert.Equal("gogearbox/gearbox", span.Tag(ext.Component))
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
}

// Test that the gearbox request context retains the tracer context
func TestChildSpan(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	reqctx := newReqCtx(200)
	gb := &GearboxContextMock{requestCtx: reqctx}
	Middleware(WithServiceName("gb"))(gb)
	_, ok := tracer.SpanFromContext(reqctx)
	assert.True(ok)
}

// Test that HTTP Status codes >= 500 get treated as error spans
func TestStatusError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	code := 500
	reqctx := newReqCtx(code)
	errMsg := "This is an error"
	wantErr := fmt.Sprintf("%d: %s", code, errMsg)
	reqctx.Error(errMsg, code)
	gb := &GearboxContextMock{requestCtx: reqctx}
	Middleware(WithServiceName("gb"))(gb)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal("gb", span.Tag(ext.ServiceName))
	assert.Equal(strconv.Itoa(code), span.Tag(ext.HTTPCode))
	assert.Equal(wantErr, span.Tag(ext.Error).(error).Error())
}

// Test that users can customize which HTTP status codes are considered an error
func TestWithStatusCheck(t *testing.T) {
	t.Run("isError", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		code := 600
		reqctx := newReqCtx(code)
		msg := "This is an error"
		reqctx.Error(msg, code)
		gb := &GearboxContextMock{requestCtx: reqctx}

		Middleware(WithServiceName("gb"), WithStatusCheck(customErrChecker))(gb)
		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]
		assert.Equal(strconv.Itoa(code), span.Tag(ext.HTTPCode))
		wantErr := fmt.Sprintf("%d: %s", code, msg)
		assert.Equal(wantErr, span.Tag(ext.Error).(error).Error())
	})
	t.Run("notError", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		code := 500
		reqctx := newReqCtx(code)
		reqctx.Error("This is an error", code)
		gb := &GearboxContextMock{requestCtx: reqctx}

		Middleware(WithServiceName("gb"), WithStatusCheck(customErrChecker))(gb)
		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]
		assert.Equal(strconv.Itoa(code), span.Tag(ext.HTTPCode))
		assert.Nil(span.Tag(ext.Error))
	})
}

// Test that users can customize how resource_name is determined
func TestCustomResourceNamer(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	reqctx := newReqCtx(200)
	gb := &GearboxContextMock{requestCtx: reqctx}

	Middleware(WithResourceNamer(resourceNamer))(gb)
	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal(customRsc, span.Tag(ext.ResourceName))
}

// Test that the trace middleware passes the context off to the next handler in the req chain even if the request is not instrumented
// MTOFF: I can't create a real http request, but I can simulate middleware being run
// by checking that the context's `index` has been incremented
func TestWithIgnoreRequest(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	reqctx := newReqCtx(200)
	// explicitly giving gb.index a value, so we can see it increment clearly
	gb := &GearboxContextMock{requestCtx: reqctx, index: 0}

	Middleware(WithIgnoreRequest(ignoreResources))(gb)
	assert.Len(mt.FinishedSpans(), 0)
	assert.Equal(1, gb.index)
}
func TestPropagation(t *testing.T) {
	t.Run("inject-extract", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		reqctx := newReqCtx(200)
		gb := &GearboxContextMock{requestCtx: reqctx}
		fcc := &gearboxutil.FasthttpCarrier{ReqHeader: &gb.Context().Request.Header}

		pspan := tracer.StartSpan("test")
		err := tracer.Inject(pspan.Context(), fcc)
		if err != nil {
			t.Fatalf("Trace injection failed")
		}
		Middleware(WithServiceName("gb"))(gb)
		sctx, err := tracer.Extract(fcc)
		if err != nil {
			t.Fatalf("Trace extraction failed")
		}
		assert.Equal(sctx.TraceID(), pspan.Context().TraceID())
		assert.Equal(sctx.SpanID(), pspan.Context().SpanID())
	})
	t.Run("req-context", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		reqctx := newReqCtx(200)
		gb := &GearboxContextMock{requestCtx: reqctx}
		fcc := &gearboxutil.FasthttpCarrier{ReqHeader: &gb.Context().Request.Header}

		pspan := tracer.StartSpan("test")
		err := tracer.Inject(pspan.Context(), fcc)
		if err != nil {
			t.Fatalf("Trace injection failed")
		}
		Middleware(WithServiceName("gb"))(gb)
		span, ok := tracer.SpanFromContext(gb.Context())
		assert.True(ok)
		assert.Equal(span.(mocktracer.Span).TraceID(), pspan.(mocktracer.Span).TraceID())
		assert.Equal(span.(mocktracer.Span).ParentID(), pspan.(mocktracer.Span).SpanID())
	})
}
func BenchmarkGearboxMiddleware(b *testing.B) {
	mt := mocktracer.Start()
	defer mt.Stop()

	reqctx := newReqCtx(200)
	gb := &GearboxContextMock{requestCtx: reqctx}
	for i := 0; i < b.N; i++ {
		Middleware()(gb)
	}
}
func BenchmarkGearboxMiddlewareWithOptions(b *testing.B) {
	mt := mocktracer.Start()
	defer mt.Stop()

	reqctx := newReqCtx(200)
	gb := &GearboxContextMock{requestCtx: reqctx}
	for i := 0; i < b.N; i++ {
		Middleware(WithServiceName("gb"), WithStatusCheck(customErrChecker), WithResourceNamer(resourceNamer), WithIgnoreRequest(ignoreResources))(gb)
	}
}

// BenchmarkGearbox is intended to serve as a comparison between gearbox with trace middleware v other middleware.
// MTOFF: Not sure if this is the right approach especially since the other benchmarks use GearboxContextMock
func BenchmarkGearbox(b *testing.B) {
	gb := gearbox.New()
	logMiddleware := func(ctx gearbox.Context) {
		fmt.Println("log message!")
		ctx.Next()
	}
	gb.Use(logMiddleware)
}

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

// GearboxContextMock provides a mock implementation the Gearbox library to be used in testing.
// It fulfills the Gearbox.Context interface and its methods mock some of the core behavior of
// Gearbox.Context when handling real HTTP traffic, without needing to spin up a server.
// See: https://pkg.go.dev/github.com/gogearbox/gearbox#Context
type GearboxContextMock struct {
	requestCtx *fasthttp.RequestCtx
	index      int
}

func (g *GearboxContextMock) Next() {
	g.index++
}
func (g *GearboxContextMock) Context() (c *fasthttp.RequestCtx) {
	return g.requestCtx
}
func (g *GearboxContextMock) Param(key string) (v string) {
	return v
}
func (g *GearboxContextMock) Query(key string) (v string) {
	return v
}
func (g *GearboxContextMock) SendBytes(value []byte) (c gearbox.Context) {
	return c
}
func (g *GearboxContextMock) SendString(value string) gearbox.Context {
	g.requestCtx.SetBodyString(value)
	return g
}
func (g *GearboxContextMock) SendJSON(in interface{}) (e error) {
	return e
}
func (g *GearboxContextMock) Status(status int) (c gearbox.Context) {
	return c
}
func (g *GearboxContextMock) Set(key string, value string) {
	g.requestCtx.Response.Header.Set(key, value)
}
func (g *GearboxContextMock) Get(key string) (v string) {
	return v
}
func (g *GearboxContextMock) SetLocal(key string, value interface{}) {
	g.requestCtx.SetUserValue(key, value)
}
func (g *GearboxContextMock) GetLocal(key string) (i interface{}) {
	return g.requestCtx.UserValue(key)
}
func (g *GearboxContextMock) Body() (b string) {
	return b
}
func (g *GearboxContextMock) ParseBody(out interface{}) (e error) {
	return e
}
