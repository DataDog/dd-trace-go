// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"

	fasthttptrace "github.com/DataDog/dd-trace-go/contrib/valyala/fasthttp/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var fasthttpTest = harness.TestCase{
	Name: instrumentation.PackageValyalaFastHTTP,
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []fasthttptrace.Option
		if serviceOverride != "" {
			opts = append(opts, fasthttptrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		handler := fasthttptrace.WrapHandler(func(ctx *fasthttp.RequestCtx) {
			ctx.SetStatusCode(200)
		}, opts...)

		ln := fasthttputil.NewInmemoryListener()
		defer ln.Close()
		go fasthttp.Serve(ln, handler)

		c := &fasthttp.Client{
			Dial: func(addr string) (net.Conn, error) {
				return ln.Dial()
			},
		}
		req := fasthttp.AcquireRequest()
		req.SetRequestURI("http://localhost/200")
		req.Header.SetMethod("GET")
		resp := fasthttp.AcquireResponse()
		err := c.Do(req, resp)
		require.NoError(t, err)

		return mt.FinishedSpans()
	},
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"fasthttp"},
		DDService:       []string{"fasthttp"},
		ServiceOverride: []string{harness.TestServiceOverride},
	},
	WantServiceSource: harness.ServiceSourceAssertions{
		Defaults:        []string{string(instrumentation.PackageValyalaFastHTTP)},
		ServiceOverride: []string{instrumentation.ServiceSourceWithServiceOption},
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "http.request", spans[0].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		// fasthttp computes the span name at config init time via instr.OperationName,
		// so it doesn't pick up v1 schema changes set via t.Setenv after config creation.
		require.Len(t, spans, 1)
		assert.Equal(t, "http.request", spans[0].OperationName())
	},
}
