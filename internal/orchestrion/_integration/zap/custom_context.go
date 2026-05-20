// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package zap

// TestCaseCustomContext verifies that trace correlation works when the calling
// function has a parameter that is not *http.Request but implements Context()
// context.Context (e.g. a custom request type).

import (
	"bytes"
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseCustomContext struct {
	logger *zap.Logger
	logs   *bytes.Buffer
}

func (tc *TestCaseCustomContext) Setup(_ context.Context, _ *testing.T) {
	tc.logs = new(bytes.Buffer)
	tc.logger = zap.New(newJSONCore(tc.logs))
}

func (tc *TestCaseCustomContext) Run(ctx context.Context, t *testing.T) {
	runTest(ctx, t, tc.logs, func(ctx context.Context) {
		carrier := &customContextCarrier{ctx: ctx}
		logWithCustomContext(carrier, tc.logger)
	})
}

func (*TestCaseCustomContext) ExpectedTraces() trace.Traces { return expectedTraces() }

// customContextCarrier is a non-http type that carries a context via Context().
type customContextCarrier struct {
	ctx context.Context
}

func (c *customContextCarrier) Context() context.Context { return c.ctx }

// logWithCustomContext has a *customContextCarrier but no context.Context parameter.
// The aspect should fall back to carrier.Context() to obtain the trace context.
func logWithCustomContext(carrier *customContextCarrier, logger *zap.Logger) {
	logger.Info("custom context request")
}
