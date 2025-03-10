// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package nethttp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type base struct {
	srv     *http.Server
	handler http.Handler
}

func (b *base) Setup(_ context.Context, t *testing.T) {
	b.srv = &http.Server{
		Addr:         fmt.Sprintf("127.0.0.1:%d", net.FreePort(t)),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	b.srv.Handler = b.handler

	go func() { assert.ErrorIs(t, b.srv.ListenAndServe(), http.ErrServerClosed) }()
	t.Cleanup(func() {
		// Using a new 10s-timeout context, as we may be running cleanup after the original context expired.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, b.srv.Shutdown(ctx))
	})
}

func (b *base) Run(_ context.Context, t *testing.T) {
	resp, err := http.Get(fmt.Sprintf("http://%s/", b.srv.Addr))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func (b *base) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /",
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
						"resource": "GET /",
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
								"resource": "POST /hit",
								"type":     "http",
							},
							Meta: map[string]string{
								"http.url":                 fmt.Sprintf("http://%s/hit", b.srv.Addr),
								"component":                "net/http",
								"span.kind":                "client",
								"network.destination.name": "127.0.0.1",
								"http.status_code":         "200",
								"http.method":              "POST",
							},
							Children: trace.Traces{
								{
									Tags: map[string]any{
										"name":     "http.request",
										"resource": "POST /hit",
										"type":     "web",
									},
									Meta: map[string]string{
										"http.useragent":   "Go-http-client/1.1",
										"http.status_code": "200",
										"http.host":        b.srv.Addr,
										"component":        "net/http",
										"http.url":         fmt.Sprintf("http://%s/hit", b.srv.Addr),
										"http.method":      "POST",
										"span.kind":        "server",
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (b *base) serveMuxHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/hit", b.handleHit)
	mux.HandleFunc("/", b.handleRoot)
	return mux
}

func (b *base) handleRoot(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	resp, err := http.Post(fmt.Sprintf("http://%s/hit", b.srv.Addr), "text/plain", r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(err.Error()))
		return
	}
	defer resp.Body.Close()

	bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(err.Error()))
		return
	}

	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(bytes)
}

func (*base) handleHit(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	b, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(err.Error()))
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}
