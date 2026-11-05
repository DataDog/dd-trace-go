// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp builds slog loggers from package main, which the harness
// suites never build. TestLoggersInMain builds and runs it.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	slogtrace "github.com/DataDog/dd-trace-go/contrib/log/slog/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func main() {
	span, ctx := tracer.StartSpanFromContext(context.Background(), "mainapp")
	// Each record carries the IDs it must hold, for the test to compare.
	want := []any{
		"want_trace_id", span.Context().TraceID(),
		"want_span_id", strconv.FormatUint(span.Context().SpanID(), 10),
	}

	slog.New(slog.NewJSONHandler(os.Stdout, nil)).InfoContext(ctx, "main-new", want...)
	slog.New(slogtrace.NewJSONHandler(os.Stdout, nil)).InfoContext(ctx, "main-contrib", want...)
	logAliased(ctx, want)
	// Instrumented builds also find the active span of the goroutine, so the
	// span must be finished first.
	span.Finish()
	slog.New(slog.NewJSONHandler(os.Stdout, nil)).InfoContext(context.Background(), "main-no-span")

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}
