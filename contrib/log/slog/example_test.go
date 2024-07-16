// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package slog_test

import (
	"context"
	"log/slog"
	"os"

	slogtrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/log/slog"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func ExampleNewJSONHandler() {
	// start the DataDog tracer
	tracer.Start()
	defer tracer.Stop()

	// create the application logger
	logger := slog.New(slogtrace.NewJSONHandler(os.Stdout, nil))

	// start a new span
	span, ctx := tracer.StartSpanFromContext(context.Background(), "ExampleNewJSONHandler")
	defer span.Finish()

	// log a message using the context containing span information
	logger.Log(ctx, slog.LevelInfo, "this is a log with tracing information")
}

func ExampleWrapHandler() {
	// start the DataDog tracer
	tracer.Start()
	defer tracer.Stop()

	// create the application logger
	myHandler := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(slogtrace.WrapHandler(myHandler))

	// start a new span
	span, ctx := tracer.StartSpanFromContext(context.Background(), "ExampleWrapHandler")
	defer span.Finish()

	// log a message using the context containing span information
	logger.Log(ctx, slog.LevelInfo, "this is a log with tracing information")
}
