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

// TestCaseGlobalLogger tests Datadog trace correlation when using the zap
// global logger functions (zap.L()).
type TestCaseGlobalLogger struct {
	logs *bytes.Buffer
}

func (tc *TestCaseGlobalLogger) Setup(_ context.Context, t *testing.T) {
	tc.logs = new(bytes.Buffer)
	restore := zap.ReplaceGlobals(zap.New(newJSONCore(tc.logs)))
	t.Cleanup(restore)
}

func (tc *TestCaseGlobalLogger) Run(ctx context.Context, t *testing.T) {
	runTest(ctx, t, tc.logs, logWithGlobalLogger)
}

func (*TestCaseGlobalLogger) ExpectedTraces() trace.Traces { return expectedTraces() }

func logWithGlobalLogger(ctx context.Context) {
	zap.L().Debug("debug message")
	zap.L().Info("info message")
	zap.L().Warn("warn message")
	zap.L().Error("error message")
}
