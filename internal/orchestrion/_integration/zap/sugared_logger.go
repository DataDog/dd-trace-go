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

// TestCaseSugaredLogger tests Datadog trace correlation when using the zap
// global sugared logger (zap.S()), covering Print-style, structured, and
// printf calling conventions.
type TestCaseSugaredLogger struct {
	logs *bytes.Buffer
}

func (tc *TestCaseSugaredLogger) Setup(_ context.Context, t *testing.T) {
	tc.logs = new(bytes.Buffer)
	restore := zap.ReplaceGlobals(zap.New(newJSONCore(tc.logs)))
	t.Cleanup(restore)
}

func (tc *TestCaseSugaredLogger) Run(ctx context.Context, t *testing.T) {
	runTest(ctx, t, tc.logs, logWithSugaredLogger)
}

func (*TestCaseSugaredLogger) ExpectedTraces() trace.Traces { return expectedTraces() }

func logWithSugaredLogger(ctx context.Context) {
	zap.S().Info("info message")
	zap.S().Infow("structured message", "key", "value", "count", 42)
	zap.S().Infof("formatted %s", "message")
}
