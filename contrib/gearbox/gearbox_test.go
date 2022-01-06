package gearbox

import (
	"testing"

	"github.com/gogearbox/gearbox"
	"github.com/valyala/fasthttp"
)

func TestDatadog(t *testing.T) {

	t.Parallel()

	t.Run("finish with error ", func(t *testing.T) {

		request := &fasthttp.RequestCtx{}
		request.URI().SetPath("/any")
		request.Request.Header.SetMethod("post")
		request.Response.SetStatusCode(500)

		gb := &GearboxContextMock{
			RequestCtx:  request,
			LocalParams: make(map[string]interface{}),
		}

		Datadog(gb)

	})

	t.Run("finish without error ", func(t *testing.T) {

		request := &fasthttp.RequestCtx{}
		request.URI().SetPath("/any")
		request.Request.Header.SetMethod("post")

		gb := &GearboxContextMock{
			RequestCtx:  request,
			LocalParams: make(map[string]interface{}),
			Headers:     make(map[string]string),
		}

		gb.Set("X-Datadog-Trace-Id", "any-trace-id")

		Datadog(gb)

	})

}

type GearboxContextMock struct {
	StatusCode  int
	QueryParams map[string]string
	Headers     map[string]string
	LocalParams map[string]interface{}
	RequestCtx  *fasthttp.RequestCtx
}

func (ctx GearboxContextMock) Next() {
}

func (ctx GearboxContextMock) Context() *fasthttp.RequestCtx {
	return ctx.RequestCtx
}

func (ctx GearboxContextMock) Param(key string) string {
	return "test"
}
func (ctx GearboxContextMock) Query(key string) string {
	return ctx.QueryParams[key]
}

func (ctx *GearboxContextMock) SendBytes(value []byte) gearbox.Context {
	return nil
}
func (ctx *GearboxContextMock) SendString(value string) gearbox.Context {
	return nil
}
func (ctx *GearboxContextMock) SendJSON(in interface{}) error {
	return nil
}

func (ctx *GearboxContextMock) Status(status int) gearbox.Context {
	ctx.StatusCode = status
	return ctx
}
func (ctx *GearboxContextMock) Set(key string, value string) {
	ctx.Headers[key] = value
}
func (ctx *GearboxContextMock) Get(key string) string {
	return ctx.Headers[key]
}
func (ctx *GearboxContextMock) SetLocal(key string, value interface{}) {
	ctx.LocalParams[key] = value
}

func (ctx *GearboxContextMock) GetLocal(key string) interface{} {
	return ctx.LocalParams[key]
}
func (ctx *GearboxContextMock) Body() string {
	return "test"
}
func (ctx *GearboxContextMock) ParseBody(out interface{}) error {
	return nil
}
