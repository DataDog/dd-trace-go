// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gearbox

import (
	"fmt"
	"testing"

	"github.com/gogearbox/gearbox"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
)

func TestTrace200(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	var reqctx fasthttp.RequestCtx
	reqctx.URI().SetPath("/any")
	reqctx.Request.Header.SetMethod("GET")
	reqctx.Response.SetStatusCode(200)
	gb := &GearboxContextMock{requestCtx: &reqctx}
	Middleware("gb")(gb)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Contains(span.Tag(ext.ResourceName), "GET /any")
	assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
	assert.Equal("gb", span.Tag(ext.ServiceName))
	assert.Equal("200", span.Tag(ext.HTTPCode))
	assert.Equal("GET", span.Tag(ext.HTTPMethod))
	// TODO: Set a domain on the req context for this test.
	// assert.Equal("http://example.com/any", span.Tag(ext.HTTPURL))
	assert.Equal("gogearbox/gearbox", span.Tag(ext.Component))
	assert.Equal(ext.SpanKindServer, span.Tag(ext.SpanKind))
}

func TestChildSpan(t *testing.T) {
	// assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	var reqctx *fasthttp.RequestCtx
	reqctx.URI().SetPath("/any")
	reqctx.Request.Header.SetMethod("GET")
	reqctx.Response.SetStatusCode(200)
	gb := &GearboxContextMock{requestCtx: reqctx}
	Middleware("gb")(gb)
	fmt.Println("MTOFF", reqctx)
	// _, ok := tracer.SpanFromContext(reqctx)
	// assert.True(ok)
}

type GearboxContextMock struct {
	requestCtx  *fasthttp.RequestCtx
}

func (g GearboxContextMock) Next() {}
func (g GearboxContextMock) Context() (c *fasthttp.RequestCtx) {
	return g.requestCtx
}
func (g GearboxContextMock) Param(key string) (v string) {
	return v
}
func (g GearboxContextMock) Query(key string) (v string) {
	return v
}
func (g GearboxContextMock) SendBytes(value []byte) (c gearbox.Context) {
	return c
}
func (g GearboxContextMock) SendString(value string) (c gearbox.Context) {
	return c
}
func (g GearboxContextMock) SendJSON(in interface{}) (e error) {
	return e
}
func (g GearboxContextMock) Status(status int) (c gearbox.Context) {
	return c
}
func (g GearboxContextMock) Set(key string, value string) {}
func (g GearboxContextMock) Get(key string) (v string) {
	return v
}
func (g GearboxContextMock) SetLocal(key string, value interface{}) {}
func (g GearboxContextMock) GetLocal(key string) (i interface{}) {
	return i
}
func (g GearboxContextMock) Body() (b string) {
	return b
}
func (g GearboxContextMock) ParseBody(out interface{}) (e error) {
	return e
}



