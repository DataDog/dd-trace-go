// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package zap

// TestCaseValueReceiver verifies that trace correlation works when the logger
// is held as a value (zap.Logger) rather than a pointer (*zap.Logger).

import (
	"bytes"
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseValueReceiver struct {
	logger zap.Logger
	logs   *bytes.Buffer
}

func (tc *TestCaseValueReceiver) Setup(_ context.Context, _ *testing.T) {
	tc.logs = new(bytes.Buffer)
	tc.logger = *zap.New(newJSONCore(tc.logs))
}

func (tc *TestCaseValueReceiver) Run(ctx context.Context, t *testing.T) {
	runTest(ctx, t, tc.logs, func(ctx context.Context) {
		logWithValueLogger(ctx, tc.logger)
	})
}

func (*TestCaseValueReceiver) ExpectedTraces() trace.Traces { return expectedTraces() }

func logWithValueLogger(ctx context.Context, logger zap.Logger) {
	logger.Info("value receiver info")
	logger.Warn("value receiver warn")
}
