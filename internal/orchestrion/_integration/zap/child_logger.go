// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package zap

import (
	"bytes"
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseChildLogger tests Datadog trace correlation when using a child logger
// created via logger.With(), the most common pattern in component-based code.
type TestCaseChildLogger struct {
	logger *zap.Logger
	logs   *bytes.Buffer
}

func (tc *TestCaseChildLogger) Setup(_ context.Context, _ *testing.T) {
	tc.logs = new(bytes.Buffer)
	tc.logger = zap.New(newJSONCore(tc.logs))
}

func (tc *TestCaseChildLogger) Run(ctx context.Context, t *testing.T) {
	runTest(ctx, t, tc.logs, func(ctx context.Context) {
		logWithChildLogger(ctx, tc.logger)
	})
}

func (*TestCaseChildLogger) ExpectedTraces() trace.Traces { return expectedTraces() }

func logWithChildLogger(ctx context.Context, logger *zap.Logger) {
	child := logger.With(zap.String("component", "test"), zap.Int("version", 1))
	child.Info("info from child")
	child.Warn("warn from child")
	child.Error("error from child")
}
