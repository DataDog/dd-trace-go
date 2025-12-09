// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package gin

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type TestCaseBase struct {
	*http.Server
}

func (tc *TestCaseBase) Setup(_ context.Context, t *testing.T) {
	gin.SetMode(gin.ReleaseMode) // Silence start-up logging
	engine := gin.New()

	tc.Server = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", net.FreePort(t)),
		Handler: engine.Handler(),
	}

	engine.GET("/ping", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"message": "pong"}) })

	go func() { assert.ErrorIs(t, tc.Server.ListenAndServe(), http.ErrServerClosed) }()
	t.Cleanup(func() {
		// Using a new 10s-timeout context, as we may be running cleanup after the original context expired.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, tc.Server.Shutdown(ctx))
	})
}

func (tc *TestCaseBase) Run(_ context.Context, t *testing.T) {
	resp, err := http.Get("http://" + tc.Server.Addr + "/ping")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func (tc *TestCaseBase) ExpectedTraces() trace.Traces {
	httpUrl := "http://" + tc.Server.Addr + "/ping"
	return trace.Traces{
		{
			// NB: 2 Top-level spans are from the HTTP Client/Server, which are library-side instrumented.
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /ping",
				"service":  "gin.test",
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
						"service":  "http.router",
						"type":     "web",
					},
					Meta: map[string]string{
						"http.url":  httpUrl,
						"component": "net/http",
						"span.kind": "server",
					},

					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name":     "http.request",
								"resource": "GET /ping",
								"service":  "gin.router",
								"type":     "web",
							},
							Meta: map[string]string{
								"http.url":  httpUrl,
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
