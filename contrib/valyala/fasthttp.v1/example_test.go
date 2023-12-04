// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttp_test

import (
	"fmt"

	fasthttptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/valyala/fasthttp.v1"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/valyala/fasthttp"
)

func fastHTTPHandler(ctx *fasthttp.RequestCtx) {
	fmt.Fprintf(ctx, "Hello World!")
}

func Example() {
	// Start the tracer
	tracer.Start()
	defer tracer.Stop()

	// Start fasthttp server
	fasthttp.ListenAndServe(":8081", fasthttptrace.WrapHandler(fastHTTPHandler))
}

func Example_withServiceName() {
	// Start the tracer
	tracer.Start()
	defer tracer.Stop()

	// Start fasthttp server
	fasthttp.ListenAndServe(":8081", fasthttptrace.WrapHandler(fastHTTPHandler, fasthttptrace.WithServiceName("fasthttp-server")))
}
