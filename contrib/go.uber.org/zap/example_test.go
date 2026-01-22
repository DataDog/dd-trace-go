// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package zap_test

import (
	"context"

	"go.uber.org/zap"

	zaptrace "github.com/DataDog/dd-trace-go/v2/contrib/go.uber.org/zap"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func ExampleWithTraceFields() {
	// start the DataDog tracer
	tracer.Start()
	defer tracer.Stop()

	// create the application logger
	logger, _ := zap.NewProduction()

	// start a new span
	span, ctx := tracer.StartSpanFromContext(context.Background(), "ExampleWithTraceCorrelation")
	defer span.Finish()

	// log a message using the context containing span information
	zaptrace.WithTraceFields(ctx, logger).Info("this is a log with tracing information")
}
