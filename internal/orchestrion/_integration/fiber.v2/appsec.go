// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fiber

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/go-libddwaf/v5"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseAppSec struct {
	*fiber.App
	addr    string
	handled atomic.Int32
}

func (*TestCaseAppSec) PreBootstrap(_ context.Context, t *testing.T) {
	if ok, err := libddwaf.Usable(); !ok {
		t.Skip("WAF is not available:", err)
	}
	t.Setenv("DD_APPSEC_RULES", "../testdata/fiber-blocking.json")
}

func (tc *TestCaseAppSec) Setup(_ context.Context, t *testing.T) {
	// No manual middleware: this case must depend on the fiber.New advice.
	tc.App = fiber.New(fiber.Config{DisableStartupMessage: true})
	tc.App.Get("/protected", func(c *fiber.Ctx) error {
		tc.handled.Add(1)
		return c.SendString("allowed")
	})
	ln := net.FreeListener(t)
	tc.addr = ln.Addr().String()
	go func() { assert.NoError(t, tc.App.Listener(ln)) }()
	t.Cleanup(func() { assert.NoError(t, tc.App.ShutdownWithTimeout(10*time.Second)) })
}

func (tc *TestCaseAppSec) Run(_ context.Context, t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://"+tc.addr+"/protected", nil)
	require.NoError(t, err)
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(res.Body)
	require.NoError(t, res.Body.Close())
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "application/json", res.Header.Get("Content-Type"))
	require.Contains(t, string(body), "You've been blocked")
	require.Zero(t, tc.handled.Load(), "the blocked request must not reach the handler")

	req.Header.Del("X-Forwarded-For")
	res, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, err = io.ReadAll(res.Body)
	require.NoError(t, res.Body.Close())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "allowed", string(body))
	require.Equal(t, int32(1), tc.handled.Load())
}

func (*TestCaseAppSec) ExpectedTraces() trace.Traces {
	traces := make(trace.Traces, 0, 2)
	for _, blocked := range []bool{true, false} {
		resource, status := "GET /protected", "200"
		meta := map[string]string{
			"component": "gofiber/fiber.v2",
			"span.kind": "server",
		}
		if blocked {
			// The WAF blocks before Fiber selects the endpoint route.
			resource, status = "GET /", "403"
			meta["appsec.blocked"] = "true"
		}
		meta["http.status_code"] = status
		traces = append(traces, &trace.Trace{
			Tags: map[string]any{"name": "http.request", "resource": "GET /protected"},
			Meta: map[string]string{"component": "net/http", "span.kind": "client", "http.status_code": status},
			Children: trace.Traces{{
				// A direct child also checks that fasthttp did not add another span.
				Tags: map[string]any{"name": "http.request", "resource": resource, "type": "web"},
				Meta: meta,
			}},
		})
	}
	return traces
}
