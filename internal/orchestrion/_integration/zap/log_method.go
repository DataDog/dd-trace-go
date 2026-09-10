// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package zap

// TestCaseLogMethod and TestCaseSugaredLogMethod verify that trace correlation
// works for the generic, dynamic-level Log/Logf/Logw/Logln entrypoints, not
// just the fixed-level methods (Info, Warn, ...).

import (
	"bytes"
	"context"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseLogMethod struct {
	logger *zap.Logger
	logs   *bytes.Buffer
}

func (tc *TestCaseLogMethod) Setup(_ context.Context, _ *testing.T) {
	tc.logs = new(bytes.Buffer)
	tc.logger = zap.New(newJSONCore(tc.logs))
}

func (tc *TestCaseLogMethod) Run(ctx context.Context, t *testing.T) {
	runTest(ctx, t, tc.logs, func(ctx context.Context) {
		logWithLogMethod(ctx, tc.logger)
	})
}

func (*TestCaseLogMethod) ExpectedTraces() trace.Traces { return expectedTraces() }

func logWithLogMethod(ctx context.Context, logger *zap.Logger) {
	logger.Log(zapcore.InfoLevel, "dynamic level message")
}

type TestCaseSugaredLogMethod struct {
	logs *bytes.Buffer
}

func (tc *TestCaseSugaredLogMethod) Setup(_ context.Context, t *testing.T) {
	tc.logs = new(bytes.Buffer)
	restore := zap.ReplaceGlobals(zap.New(newJSONCore(tc.logs)))
	t.Cleanup(restore)
}

func (tc *TestCaseSugaredLogMethod) Run(ctx context.Context, t *testing.T) {
	runTest(ctx, t, tc.logs, logWithSugaredLogMethods)
}

func (*TestCaseSugaredLogMethod) ExpectedTraces() trace.Traces { return expectedTraces() }

func logWithSugaredLogMethods(ctx context.Context) {
	zap.S().Log(zapcore.InfoLevel, "dynamic level message")
	zap.S().Logf(zapcore.InfoLevel, "formatted %s", "message")
	zap.S().Logw(zapcore.InfoLevel, "structured message", "key", "value")
	zap.S().Logln(zapcore.InfoLevel, "line message")
}
