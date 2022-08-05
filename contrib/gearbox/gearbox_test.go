// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.
package gearbox

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	"github.com/gogearbox/gearbox"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

const (
	xDatadogParentID         = "741852"
	xDatadogTraceID          = "123456"
	xDatadogSamplingPriority = "2"
	hostTest                 = "http://apicat.com"
)

var body = make([]byte, 1000)

type requestTest struct {
	TitleTest      string
	Path           string
	Method         string
	StatusResponse int
}

func TestDatadog(t *testing.T) {

	listTest := []requestTest{
		{
			TitleTest:      "Request Post with Status 200",
			Path:           "/cat",
			Method:         "post",
			StatusResponse: 200,
		},
		{
			TitleTest:      "Request Get with Status 201",
			Path:           "/cat",
			Method:         "get",
			StatusResponse: 201,
		},
	}

	for _, test := range listTest {

		t.Run(test.TitleTest, func(t *testing.T) {

			mt := mocktracer.Start()
			defer mt.Stop()

			//First Request..
			var reqctx fasthttp.RequestCtx
			reqctx.URI().SetHost(hostTest)
			reqctx.URI().SetPath(test.Path)
			reqctx.Request.Header.SetMethod(test.Method)
			//reqctx.Request.Header.Add()
			reqctx.Response.SetStatusCode(test.StatusResponse)

			gb := &ContextMock{
				RequestCtx: &reqctx,
				//Headers:     headers,
				LocalParams: map[string]interface{}{},
			}
			gb.Context().Response.SetBody(body)
			Middleware(gb)

			spans := mt.FinishedSpans()
			assert.Len(t, spans, 1)
			spanF := spans[0]

			assert.Equal(t, ext.SpanTypeWeb, spanF.Tag(ext.SpanType))
			assert.Equal(t, test.Method, spanF.Tag(ext.HTTPMethod))
			assert.Equal(t, string(gb.RequestCtx.URI().FullURI()), spanF.Tag(ext.HTTPURL))
			assert.Equal(t, operationName, spanF.OperationName())
			assert.Equal(t, test.Path, spanF.Tag(ext.ResourceName))
			assert.Equal(t, test.StatusResponse, spanF.Tag(ext.HTTPCode))

		})

	}

}

type ContextMock struct {
	StatusCode  int
	QueryParams map[string]string
	Headers     map[string]string
	LocalParams map[string]interface{}
	RequestCtx  *fasthttp.RequestCtx
}

func (_ ContextMock) Next() {}

func (ctx ContextMock) Context() *fasthttp.RequestCtx {
	return ctx.RequestCtx
}

func (_ ContextMock) Param(_ string) string {
	return "test"
}

func (ctx ContextMock) Query(key string) string {
	return ctx.QueryParams[key]
}

func (ctx *ContextMock) SendBytes(_ []byte) gearbox.Context {
	return ctx
}

func (ctx *ContextMock) SendString(_ string) gearbox.Context {
	return ctx
}

func (_ ContextMock) SendJSON(_ interface{}) error {
	return nil
}

func (ctx *ContextMock) Status(status int) gearbox.Context {
	ctx.StatusCode = status
	return ctx
}

func (ctx ContextMock) Set(key string, value string) {
	ctx.Headers[key] = value
}

func (ctx ContextMock) Get(key string) string {
	return ctx.Headers[key]
}

func (ctx *ContextMock) SetLocal(key string, value interface{}) {
	ctx.LocalParams[key] = value
}

func (ctx *ContextMock) GetLocal(key string) interface{} {
	return ctx.LocalParams[key]
}

func (_ ContextMock) Body() string {
	return "test"
}

func (_ ContextMock) ParseBody(out interface{}) error {
	return nil
}
