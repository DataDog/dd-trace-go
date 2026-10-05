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

// TestCaseNewLogger tests Datadog trace correlation when using zap.New() to
// construct a logger and calling its log methods directly.
type TestCaseNewLogger struct {
	logger *zap.Logger
	logs   *bytes.Buffer
}

func (tc *TestCaseNewLogger) Setup(_ context.Context, _ *testing.T) {
	tc.logs = new(bytes.Buffer)
	tc.logger = zap.New(newJSONCore(tc.logs))
}

func (tc *TestCaseNewLogger) Run(ctx context.Context, t *testing.T) {
	runTest(ctx, t, tc.logs, func(ctx context.Context) {
		logWithLogger(ctx, tc.logger)
	})
}

func (*TestCaseNewLogger) ExpectedTraces() trace.Traces { return expectedTraces() }

func logWithLogger(ctx context.Context, logger *zap.Logger) {
	logger.Debug("debug message")
	logger.Info("info message")
	logger.Warn("warn message")
	logger.Error("error message")
}
