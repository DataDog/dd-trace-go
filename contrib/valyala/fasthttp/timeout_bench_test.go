// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttp

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

// BenchmarkTimeoutHandler measures a traced request that completes before its
// deadline. The native fasthttp wrapper does not synchronize with tracing or
// AppSec. Its result shows the added cost; it is not an equivalent option.
func BenchmarkTimeoutHandler(b *testing.B) {
	require.NoError(b, tracer.Start(tracer.WithLogger(testutils.DiscardLogger())))
	defer tracer.Stop()

	app := func(ctx *fasthttp.RequestCtx) { ctx.SetBodyString("ok") }
	for _, bc := range []struct {
		name    string
		handler fasthttp.RequestHandler
	}{
		{"no-timeout", WrapHandler(app)},
		{"native", WrapHandler(fasthttp.TimeoutHandler(app, time.Minute, "timeout"))},
		{"wrap-outside", WrapHandler(TimeoutHandler(app, time.Minute, "timeout"))},
		{"wrap-inside", TimeoutHandler(WrapHandler(app), time.Minute, "timeout")},
	} {
		b.Run(bc.name, func(b *testing.B) {
			var req fasthttp.Request
			req.SetRequestURI("http://example.test/path?query=value")
			req.Header.Set("User-Agent", "benchmark")
			var ctx fasthttp.RequestCtx
			ctx.Init(&req, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}, nil)
			b.ReportAllocs()
			for b.Loop() {
				bc.handler(&ctx)
				ctx.Response.Reset()
			}
		})
	}
}
