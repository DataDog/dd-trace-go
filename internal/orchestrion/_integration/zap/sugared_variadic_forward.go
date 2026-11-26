// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package zap

import (
	"bytes"
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseSugaredVariadicForward is TestCaseVariadicForward for the
// SugaredLogger methods, which take ...any.
type TestCaseSugaredVariadicForward struct {
	logger *zap.SugaredLogger
	logs   *bytes.Buffer
}

func (tc *TestCaseSugaredVariadicForward) Setup(context.Context, *testing.T) {
	tc.logs = new(bytes.Buffer)
	tc.logger = zap.New(newJSONCore(tc.logs)).Sugar()
}

func (tc *TestCaseSugaredVariadicForward) Run(ctx context.Context, t *testing.T) {
	runTestExact(ctx, t, tc.logs, 3, func(ctx context.Context) {
		forwardSugared(ctx, tc.logger, "forwarded", "k", "v")
	})
}

func (*TestCaseSugaredVariadicForward) ExpectedTraces() trace.Traces { return expectedTraces() }

func forwardSugared(ctx context.Context, logger *zap.SugaredLogger, msg string, keysAndValues ...any) {
	_ = ctx
	logger.Infow(msg, keysAndValues...)
	logger.Infof(msg+" %s=%s", keysAndValues...)
	logger.Info(keysAndValues...)
}
