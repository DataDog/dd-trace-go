// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelc

import (
	"runtime"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"go.opentelemetry.io/otelc/pkg/hook/hooktest"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

func newServer() *fasthttp.Server {
	return &fasthttp.Server{Handler: func(ctx *fasthttp.RequestCtx) { ctx.SetStatusCode(fasthttp.StatusOK) }}
}

// serveOne sends one request through srv's current handler.
func serveOne(srv *fasthttp.Server) {
	var ctx fasthttp.RequestCtx
	ctx.Request.SetRequestURI("/ping")
	srv.Handler(&ctx)
}

func TestBeforeServeConcurrent(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	srv := newServer()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			BeforeServe(hooktest.NewMockHookContext(srv, nil), srv, nil)
			serveOne(srv)
		})
	}
	wg.Wait()

	// A handler wrapped more than once would report more than one span per
	// request.
	assert.Len(t, mt.FinishedSpans(), 8)
}

func TestSkipServer(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	srv := newServer()
	SkipServer(srv)
	BeforeServe(hooktest.NewMockHookContext(srv, nil), srv, nil)
	serveOne(srv)
	assert.Empty(t, mt.FinishedSpans())
}

func TestHandledFreesServer(t *testing.T) {
	srv := newServer()
	key := weak.Make(srv)
	BeforeServe(hooktest.NewMockHookContext(srv, nil), srv, nil)
	_, ok := handled.Load(key)
	require.True(t, ok)

	srv = nil
	assert.Eventually(t, func() bool {
		runtime.GC()
		_, ok := handled.Load(key)
		return key.Value() == nil && !ok
	}, 5*time.Second, 10*time.Millisecond)
}
