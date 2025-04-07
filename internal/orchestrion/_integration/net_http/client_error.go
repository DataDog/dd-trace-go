// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package nethttp

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCaseClientError checks of the net/http client instrumentation handles creates error if the returned status code is 4xx.
type TestCaseClientError struct {
	srv *http.Server
}

func (b *TestCaseClientError) Setup(_ context.Context, t *testing.T) {
	b.srv = &http.Server{
		Addr:         fmt.Sprintf("127.0.0.1:%d", net.FreePort(t)),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	b.srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	go func() { assert.ErrorIs(t, b.srv.ListenAndServe(), http.ErrServerClosed) }()
	t.Cleanup(func() {
		// Using a new 10s-timeout context, as we may be running cleanup after the original context expired.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, b.srv.Shutdown(ctx))
	})
}

func (b *TestCaseClientError) Run(_ context.Context, t *testing.T) {
	resp, err := http.Get(fmt.Sprintf("http://%s/", b.srv.Addr))
	require.NoError(t, err)
	require.Equal(t, http.StatusTeapot, resp.StatusCode)
}

func (*TestCaseClientError) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /",
				"type":     "http",
			},
			Meta: map[string]string{
				"component":        "net/http",
				"span.kind":        "client",
				"http.errors":      "418 I'm a teapot",
				"http.status_code": "418",
			},
		},
	}
}
