// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package chiv5

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/internal/orchestrion/_integration/internal/trace"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type TestCase struct {
	*http.Server
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	router := chi.NewRouter()

	tc.Server = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", net.FreePort(t)),
		Handler: router,
	}

	router.Get("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})

	go func() { assert.ErrorIs(t, tc.Server.ListenAndServe(), http.ErrServerClosed) }()
	t.Cleanup(func() {
		// Using a new 10s-timeout context, as we may be running cleanup after the original context expired.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, tc.Server.Shutdown(ctx))
	})
}

func (tc *TestCase) Run(_ context.Context, t *testing.T) {
	resp, err := http.Get(fmt.Sprintf("http://%s/", tc.Server.Addr))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func (tc *TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			// NB: 2 Top-level spans are from the HTTP Client/Server, which are library-side instrumented.
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /",
				"service":  "chi.v5.test",
				"type":     "http",
			},
			Meta: map[string]string{
				"http.url":  fmt.Sprintf("http://%s/", tc.Server.Addr),
				"component": "net/http",
				"span.kind": "client",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "http.request",
						"resource": "GET /",
						"service":  "http.router",
						"type":     "web",
					},
					Meta: map[string]string{
						"http.url":  fmt.Sprintf("http://%s/", tc.Server.Addr),
						"component": "net/http",
						"span.kind": "server",
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name":     "http.request",
								"resource": "GET /",
								"service":  "chi.router",
								"type":     "web",
							},
							Meta: map[string]string{
								"http.url":  fmt.Sprintf("http://%s/", tc.Server.Addr),
								"component": "go-chi/chi.v5",
								"span.kind": "server",
							},
							Children: nil,
						},
					},
				},
			},
		},
	}
}
