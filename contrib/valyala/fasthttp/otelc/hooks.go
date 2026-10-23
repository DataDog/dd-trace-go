// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc rules and hooks for github.com/valyala/fasthttp.
// See otelc.yaml. It is a separate module so otelc never reaches contrib users
// who do not use it.
package otelc

import (
	"net"
	"runtime"
	"sync"
	"weak"

	"github.com/valyala/fasthttp"
	"go.opentelemetry.io/otelc/pkg/hook"

	fasthttptrace "github.com/DataDog/dd-trace-go/contrib/valyala/fasthttp/v2"
)

var (
	// mu serializes SkipServer and the hooks, so a server served on several
	// listeners or connections at once is wrapped once and no Serve reads the
	// handler while it is replaced. The hooks run once per listener or
	// connection, never per request.
	mu sync.Mutex
	// handled holds the servers whose handler is already wrapped, or must not
	// be. An entry is deleted once its server is freed.
	handled sync.Map // weak.Pointer[fasthttp.Server] -> struct{}
)

// markHandled records srv in handled. The caller holds mu.
func markHandled(srv *fasthttp.Server) {
	key := weak.Make(srv)
	if _, loaded := handled.LoadOrStore(key, struct{}{}); !loaded {
		runtime.AddCleanup(srv, func(key weak.Pointer[fasthttp.Server]) { handled.Delete(key) }, key)
	}
}

// SkipServer keeps the fasthttp rules from wrapping srv's handler. Frameworks
// that trace their requests themselves, such as Fiber, call it for the server
// they own, so each request gets one span.
func SkipServer(srv *fasthttp.Server) {
	if srv == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	markHandled(srv)
}

// BeforeServe wraps the server's handler the first time the server is served.
// A server served on several listeners is wrapped once.
func BeforeServe(_ hook.HookContext, srv *fasthttp.Server, _ net.Listener) {
	wrapServer(srv)
}

// BeforeServeConn does the same as BeforeServe for a connection served with
// ServeConn, which does not call Serve.
func BeforeServeConn(_ hook.HookContext, srv *fasthttp.Server, _ net.Conn) {
	wrapServer(srv)
}

// wrapServer wraps srv's handler unless it is already wrapped or skipped.
//
// A call before the contrib's init panics in WrapHandler. The hook trampoline
// recovers it and the server is served untraced. The server is only recorded
// once its handler is wrapped, so a later call wraps it.
func wrapServer(srv *fasthttp.Server) {
	if srv == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if srv.Handler == nil {
		return
	}
	if _, ok := handled.Load(weak.Make(srv)); ok {
		return
	}
	srv.Handler = fasthttptrace.WrapHandler(srv.Handler)
	markHandled(srv)
}
