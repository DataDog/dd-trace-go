// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package zap_test

import (
	"context"

	"go.uber.org/zap"

	ddzap "github.com/DataDog/dd-trace-go/contrib/go.uber.org/zap/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func ExampleTraceFields() {
	_ = tracer.Start()
	defer tracer.Stop()

	logger, _ := zap.NewProduction()
	defer logger.Sync()

	span, ctx := tracer.StartSpanFromContext(context.Background(), "mySpan")
	defer span.Finish()

	// Use TraceFields to add Datadog trace correlation fields to logs
	logger.Info("completed request", ddzap.TraceFields(ctx)...)
}
