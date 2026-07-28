// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package zap

// TestCaseGoroutine verifies that trace correlation works when logging from a
// goroutine that receives a context.Context as a parameter. This distinguishes
// call-site injection (which works correctly) from GLS-based approaches (which
// are unreliable across goroutine boundaries).

import (
	"bytes"
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseGoroutine struct {
	logger *zap.Logger
	logs   *bytes.Buffer
}

func (tc *TestCaseGoroutine) Setup(_ context.Context, _ *testing.T) {
	tc.logs = new(bytes.Buffer)
	tc.logger = zap.New(newJSONCore(tc.logs))
}

func (tc *TestCaseGoroutine) Run(ctx context.Context, t *testing.T) {
	runTest(ctx, t, tc.logs, func(ctx context.Context) {
		done := make(chan struct{})
		go logInGoroutine(ctx, tc.logger, done)
		<-done
	})
}

func (*TestCaseGoroutine) ExpectedTraces() trace.Traces { return expectedTraces() }

func logInGoroutine(ctx context.Context, logger *zap.Logger, done chan<- struct{}) {
	defer close(done)
	logger.Info("from goroutine")
	logger.Warn("warn from goroutine")
}
