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
	"sync"
	"weak"

	"github.com/valyala/fasthttp"
	"go.opentelemetry.io/otelc/pkg/hook"

	fasthttptrace "github.com/DataDog/dd-trace-go/contrib/valyala/fasthttp/v2"
)

// handled holds the servers whose handler is already wrapped, or must not be.
// Weak keys let a server be freed once nothing else uses it.
var handled sync.Map // weak.Pointer[fasthttp.Server] -> struct{}

// SkipServer keeps the fasthttp rules from wrapping srv's handler. Frameworks
// that trace their requests themselves, such as Fiber, call it for the server
// they own, so each request gets one span.
func SkipServer(srv *fasthttp.Server) {
	if srv != nil {
		handled.Store(weak.Make(srv), struct{}{})
	}
}

// BeforeServe wraps the server's handler the first time the server is served.
// A server served on several listeners is wrapped once.
func BeforeServe(_ hook.HookContext, srv *fasthttp.Server, _ net.Listener) {
	if srv == nil || srv.Handler == nil {
		return
	}
	if _, loaded := handled.LoadOrStore(weak.Make(srv), struct{}{}); loaded {
		return
	}
	srv.Handler = fasthttptrace.WrapHandler(srv.Handler)
}
