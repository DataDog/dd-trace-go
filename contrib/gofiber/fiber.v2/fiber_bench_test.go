// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fiber

import (
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func BenchmarkFiberMiddleware(b *testing.B) {
	for _, tc := range []struct {
		name   string
		appsec bool
	}{{"appsec-off", false}, {"appsec-on", true}} {
		b.Run(tc.name, func(b *testing.B) {
			b.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
			require.NoError(b, tracer.Start(
				tracer.WithTestDefaults(nil),
				tracer.WithLogger(testutils.DiscardLogger()),
				tracer.WithLogStartup(false),
				tracer.WithAppSecEnabled(tc.appsec),
			))
			b.Cleanup(tracer.Stop)
			if tc.appsec && !instr.AppSecEnabled() {
				b.Skip("WAF is not available")
			}

			router := fiber.New()
			router.Use(Middleware())
			router.Get("/items/:id", func(c *fiber.Ctx) error { return c.SendString("ok") })
			handler := router.Handler()
			var request fasthttp.Request
			request.SetRequestURI("http://example.com/items/123?filter=one&filter=two")
			request.Header.SetMethod("GET")
			request.Header.Set("User-Agent", "fiber-benchmark")
			request.Header.SetCookie("session", "benign")
			var ctx fasthttp.RequestCtx
			ctx.Init(&request, nil, nil)
			handler(&ctx)
			require.Equal(b, fiber.StatusOK, ctx.Response.StatusCode())
			require.Equal(b, "ok", string(ctx.Response.Body()))
			b.ReportAllocs()
			for b.Loop() {
				// A reused connection must not retain the previous span or WAF context.
				ctx.ResetUserValues()
				ctx.Response.Reset()
				handler(&ctx)
			}
		})
	}
}
