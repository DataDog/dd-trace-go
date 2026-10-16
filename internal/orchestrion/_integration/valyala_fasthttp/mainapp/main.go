// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp serves fasthttp from a package main that does not import the
// contrib, so the binary links it only through otelc/all. TestServersInMain
// builds and runs it.
package main

import (
	"fmt"
	"net"

	"github.com/valyala/fasthttp"
)

func main() {
	// (*Server).Serve called directly.
	srv := &fasthttp.Server{Handler: handler}
	ln := listen()
	go func() { _ = srv.Serve(ln) }()
	get(ln, "/server")
	if err := srv.Shutdown(); err != nil {
		panic(err)
	}

	// The package-level Serve, which builds its own Server.
	ln = listen()
	go func() { _ = fasthttp.Serve(ln, handler) }()
	get(ln, "/func")

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

func handler(ctx *fasthttp.RequestCtx) {
	ctx.SetBodyString("ok")
}

func listen() net.Listener {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	return ln
}

// get uses the fasthttp client, which is not traced, so the app reports only
// server spans.
func get(ln net.Listener, path string) {
	status, _, err := fasthttp.Get(nil, "http://"+ln.Addr().String()+path)
	if err != nil {
		panic(err)
	}
	if status != fasthttp.StatusOK {
		panic(fmt.Sprintf("GET %s: status %d", path, status))
	}
}
