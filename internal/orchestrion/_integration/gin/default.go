// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package gin

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDefault checks that an engine created with gin.Default is traced
// once. gin.Default calls gin.New, which must not add a second gin span.
type TestCaseDefault struct {
	*http.Server
}

func (tc *TestCaseDefault) Setup(_ context.Context, t *testing.T) {
	gin.SetMode(gin.ReleaseMode) // Silence start-up logging
	engine := gin.Default()
	engine.GET("/default", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	ln := net.FreeListener(t)
	tc.Server = &http.Server{
		Addr:    ln.Addr().String(),
		Handler: engine.Handler(),
	}

	go func() { assert.ErrorIs(t, tc.Server.Serve(ln), http.ErrServerClosed) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, tc.Server.Shutdown(ctx))
	})
}

func (tc *TestCaseDefault) Run(_ context.Context, t *testing.T) {
	resp, err := http.Get("http://" + tc.Server.Addr + "/default")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// ExpectedSpanCount is the net/http client and server spans plus one gin span.
func (*TestCaseDefault) ExpectedSpanCount() int {
	return 3
}

func (tc *TestCaseDefault) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /default",
				"type":     "http",
			},
			Meta: map[string]string{
				"component": "net/http",
				"span.kind": "client",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "http.request",
						"resource": "GET /default",
						"type":     "web",
					},
					Meta: map[string]string{
						"component": "net/http",
						"span.kind": "server",
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name":     "http.request",
								"resource": "GET /default",
								"service":  "gin.router",
								"type":     "web",
							},
							Meta: map[string]string{
								"component": "gin-gonic/gin",
								"span.kind": "server",
							},
						},
					},
				},
			},
		},
	}
}
