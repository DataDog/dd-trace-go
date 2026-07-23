// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package zap

// TestCaseVariadicForward exercises the canonical "logging wrapper" pattern in
// which a helper forwards its variadic []zap.Field argument to an inner zap
// method via the spread operator (fields...). The orchestrion rewrite must
// preserve the call's Ellipsis when wrapping the receiver chain; if it drops
// the spread, the rewritten code passes []zap.Field where zap.Field is
// expected and fails to compile.
//
// Reproduces the failure seen when building etcd via
// github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap, where
// ctxzap.Debug/Info/Warn/Error forward their variadic fields to the underlying
// *zap.Logger.

import (
	"bytes"
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseVariadicForward struct {
	logger *zap.Logger
	logs   *bytes.Buffer
}

func (tc *TestCaseVariadicForward) Setup(_ context.Context, _ *testing.T) {
	tc.logs = new(bytes.Buffer)
	tc.logger = zap.New(newJSONCore(tc.logs))
}

func (tc *TestCaseVariadicForward) Run(ctx context.Context, t *testing.T) {
	runTest(ctx, t, tc.logs, func(ctx context.Context) {
		forwardDebug(ctx, tc.logger, "debug forwarded", zap.String("k", "v"))
		forwardInfo(ctx, tc.logger, "info forwarded", zap.String("k", "v"))
		forwardWarn(ctx, tc.logger, "warn forwarded", zap.String("k", "v"))
		forwardError(ctx, tc.logger, "error forwarded", zap.String("k", "v"))
	})
}

func (*TestCaseVariadicForward) ExpectedTraces() trace.Traces { return expectedTraces() }

// The wrappers below intentionally forward fields via the spread operator.
// ctx is in scope so the orchestrion zap aspect fires on the inner call.

func forwardDebug(ctx context.Context, logger *zap.Logger, msg string, fields ...zap.Field) {
	_ = ctx
	logger.Debug(msg, fields...)
}

func forwardInfo(ctx context.Context, logger *zap.Logger, msg string, fields ...zap.Field) {
	_ = ctx
	logger.Info(msg, fields...)
}

func forwardWarn(ctx context.Context, logger *zap.Logger, msg string, fields ...zap.Field) {
	_ = ctx
	logger.Warn(msg, fields...)
}

func forwardError(ctx context.Context, logger *zap.Logger, msg string, fields ...zap.Field) {
	_ = ctx
	logger.Error(msg, fields...)
}
