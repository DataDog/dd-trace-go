// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package goka_test

import (
	"log"

	gokatrace "github.com/DataDog/dd-trace-go/contrib/lovoo/goka/v2"

	"github.com/lovoo/goka"
	"github.com/lovoo/goka/codec"
)

func Example() {
	tr := gokatrace.NewTracer(
		gokatrace.WithService("orders-processor"),
		gokatrace.WithDataStreams(),
	)

	// handle processes an input message. Because the callback is wrapped, it runs
	// inside a "kafka.consume" span, and ctx.Emit continues the trace downstream.
	handle := func(ctx goka.Context, msg any) {
		ctx.Emit("orders-enriched", ctx.Key(), msg)
	}

	g := goka.DefineGroup(
		"orders",
		goka.Input("orders", new(codec.String), tr.WrapCallback(handle)),
		goka.Output("orders-enriched", new(codec.String)),
	)

	p, err := goka.NewProcessor(
		[]string{"localhost:9092"},
		g,
		goka.WithContextWrapper(tr.WrapContext),
	)
	if err != nil {
		log.Fatal(err)
	}
	_ = p
}
