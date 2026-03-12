// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"fmt"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"

	fasthttptrace "github.com/DataDog/dd-trace-go/contrib/valyala/fasthttp/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var valyalaFastHTTP = harness.TestCase{
	Name: instrumentation.PackageValyalaFastHTTP,
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []fasthttptrace.Option
		if serviceOverride != "" {
			opts = append(opts, fasthttptrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		handler := fasthttptrace.WrapHandler(func(fctx *fasthttp.RequestCtx) {
			fmt.Fprintf(fctx, "OK")
		}, opts...)

		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		server := &fasthttp.Server{Handler: handler}
		go server.Serve(ln)
		defer server.Shutdown()

		resp, err := http.Get("http://" + ln.Addr().String() + "/test")
		require.NoError(t, err)
		defer resp.Body.Close()

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
		require.Len(t, spans, 1)
		assert.Equal(t, "http.request", spans[0].OperationName())
	},
}
