// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttp_test

import (
	"fmt"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	fasthttp2 "github.com/DataDog/dd-trace-go/v2/v2/contrib/valyala/fasthttp.v1"

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
	fasthttp.ListenAndServe(":8081", fasthttp2.WrapHandler(fastHTTPHandler))
}

func Example_withServiceName() {
	// Start the tracer
	tracer.Start()
	defer tracer.Stop()

	// Start fasthttp server
	fasthttp.ListenAndServe(":8081", fasthttp2.WrapHandler(fastHTTPHandler, fasthttp2.WithServiceName("fasthttp-server")))
}
