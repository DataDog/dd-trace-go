// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package fiber

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type TestCase struct {
	*fiber.App
	addr string
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	tc.App = fiber.New(fiber.Config{DisableStartupMessage: true})
	tc.App.Get("/ping", func(c *fiber.Ctx) error { return c.JSON(map[string]any{"message": "pong"}) })
	tc.addr = fmt.Sprintf("127.0.0.1:%d", net.FreePort(t))

	go func() { assert.NoError(t, tc.App.Listen(tc.addr)) }()
	t.Cleanup(func() {
		assert.NoError(t, tc.App.ShutdownWithTimeout(10*time.Second))
	})
}

func (tc *TestCase) Run(_ context.Context, t *testing.T) {
	resp, err := http.Get("http://" + tc.addr + "/ping")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func (tc *TestCase) ExpectedTraces() trace.Traces {
	httpUrl := "http://" + tc.addr + "/ping"
	return trace.Traces{
		{
			// NB: Top-level span is from the HTTP Client, which is library-side instrumented.
			// The net/http server-side span does not appear here because fiber uses fasthttp internally instead of net/http.
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /ping",
				"service":  "fiber.v2.test",
				"type":     "http",
			},
			Meta: map[string]string{
				"http.url":  httpUrl,
				"component": "net/http",
				"span.kind": "client",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "http.request",
						"resource": "GET /ping",
						"service":  "fiber",
						"type":     "web",
					},
					Meta: map[string]string{
						"http.url":  "/ping", // This is implemented incorrectly in the fiber.v2 dd-trace-go integration.
						"component": "gofiber/fiber.v2",
						"span.kind": "server",
					},
				},
			},
		},
	}
}
