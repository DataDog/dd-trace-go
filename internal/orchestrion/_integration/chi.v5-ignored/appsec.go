// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package chiv5ignored

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	libddwaf "github.com/DataDog/go-libddwaf/v5"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCase struct{ server *http.Server }

type routeLogCounter struct{ count atomic.Int32 }

func (c *routeLogCounter) Log(message string) {
	if strings.Contains(message, "RouteMatched called without an active HandlerOperation") {
		c.count.Add(1)
	}
}

func (*TestCase) PreBootstrap(_ context.Context, t *testing.T) {
	if ok, err := libddwaf.Usable(); !ok {
		t.Skip("WAF is not available:", err)
	}
	t.Setenv("DD_APPSEC_RULES", "../testdata/chi-path-params.json")
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	// An indirect call avoids the otelc rule without its ignore directive.
	//orchestrion:ignore
	newRouter := chi.NewRouter
	r := newRouter()
	r.Get("/users/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	ln := net.FreeListener(t)
	tc.server = &http.Server{Addr: ln.Addr().String(), Handler: r}
	go func() { assert.ErrorIs(t, tc.server.Serve(ln), http.ErrServerClosed) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, tc.server.Shutdown(ctx))
	})
}

func (tc *TestCase) Run(_ context.Context, t *testing.T) {
	counter := &routeLogCounter{}
	previousLevel := log.GetLevel()
	log.SetLevel(log.LevelDebug)
	undo := log.UseLogger(counter)
	defer func() {
		log.Flush()
		undo()
		log.SetLevel(previousLevel)
		assert.Zero(t, counter.count.Load())
	}()
	resp, err := http.Get("http://" + tc.server.Addr + "/users/attack")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func (*TestCase) ExpectedSpanCount() int { return 2 }

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{{Tags: map[string]any{"name": "http.request", "resource": "GET /users/attack"}, Meta: map[string]string{"component": "net/http", "span.kind": "client"}, Children: trace.Traces{{Meta: map[string]string{"component": "net/http", "span.kind": "server", "http.status_code": "200"}, MetaAbsent: []string{"_dd.appsec.json"}}}}}
}
