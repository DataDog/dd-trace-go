// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package fasthttpdep serves a fasthttp server from a dependency module.
package fasthttpdep

import (
	"net"

	"github.com/valyala/fasthttp"
)

// Serve serves GET /dep on ln in a new goroutine and returns the server, so
// that the caller can shut it down.
func Serve(ln net.Listener) *fasthttp.Server {
	srv := &fasthttp.Server{Handler: func(ctx *fasthttp.RequestCtx) {
		ctx.SetBodyString("dep")
	}}
	go func() { _ = srv.Serve(ln) }()
	return srv
}
