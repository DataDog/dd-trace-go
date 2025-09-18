// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package logs

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func init() {
	// Register integration functions with the tracer
	// This ensures OTLP logs are initialized when the tracer starts
	tracer.SetOTLPLogsFunctions(
		func(ctx context.Context) error {
			return StartWithTracer(ctx)
		},
		func(ctx context.Context) error {
			return StopWithTracer(ctx)
		},
		func(ctx context.Context) error {
			return FlushWithTracer(ctx)
		},
	)
}
