// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.
package gearbox

import (
	"testing"

	"github.com/gogearbox/gearbox"
	"github.com/valyala/fasthttp"
)

func TestDatadog(t *testing.T) {
	var reqctx fasthttp.RequestCtx
	reqctx.URI().SetPath("/any")
	reqctx.Request.Header.SetMethod("post")
	reqctx.Response.SetStatusCode(500)
		
	t.Run("error", func(t *testing.T) {
		gb := &GearboxContextMock{RequestCtx:  &reqctx}
		Middleware(gb)
	})

	t.Run("ok ", func(t *testing.T) {
		gb := &GearboxContextMock{RequestCtx:  reqctx}
		gb.Set("X-Datadog-Trace-Id", "any-trace-id")
		Middleware(gb)
	})
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

func (_ ContextMock) Set(key string, value string) {
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
