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
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func newReqCtx(code int) *fasthttp.RequestCtx {
	reqctx := &fasthttp.RequestCtx{}
	reqctx.URI().SetPath("/any")
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
	assert.Equal("http.request", span.OperationName())
	assert.Equal(span.Tag(ext.ResourceName), "GET /any")
	assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
	assert.Equal("gb", span.Tag(ext.ServiceName))
	assert.Equal("200", span.Tag(ext.HTTPCode))
	assert.Equal("GET", span.Tag(ext.HTTPMethod))
	// TODO: Set a domain on the req context for this test.
	// assert.Equal("http://example.com/any", span.Tag(ext.HTTPURL))
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

func TestWithStatusCheck(t *testing.T) {
	customErrChecker := func(statusCode int) bool {
		return statusCode >= 600
	}
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
func TestCustomResourceNamer(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	reqctx := newReqCtx(200)
	gb := &GearboxContextMock{requestCtx: reqctx}

	customRsc := "custom resource"
	namer := func(gctx gearbox.Context) string {
		return customRsc
	}

	Middleware(WithResourceNamer(namer))(gb)
	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal(customRsc, span.Tag(ext.ResourceName))
}

// I can't create a real http request, but I can simulate middleware being run:
// by checking that the context's `index` has been incremented
func TestWithIgnoreRequest(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	reqctx := newReqCtx(200)
	// explicitly giving gb.index a value, so we can see it increment clearly
	gb := &GearboxContextMock{requestCtx: reqctx, index: 0}

	ignoreResources := func(c gearbox.Context) bool {
		return strings.HasPrefix(string(c.Context().URI().Path()), "/any")
	}
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
		fcc := &FasthttpContextCarrier{gb.Context()}

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
		fcc := &FasthttpContextCarrier{gb.Context()}

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

// GearboxContextMock provides a mock implementation the Gearbox library to be used in testing.
// It mocks the way Gearbox.Context is interfaced with and updated when the library handles
// real HTTP traffic without needing to spin up a server.
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
