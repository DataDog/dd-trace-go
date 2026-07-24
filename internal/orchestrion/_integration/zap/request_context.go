// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package zap

// TestCaseRequestContext verifies that trace correlation works when the calling
// function has an *http.Request but no context.Context parameter directly. The
// aspect falls back to req.Context() to obtain the trace context.

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"go.uber.org/zap"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseRequestContext struct {
	logger *zap.Logger
	logs   *bytes.Buffer
}

func (tc *TestCaseRequestContext) Setup(_ context.Context, _ *testing.T) {
	tc.logs = new(bytes.Buffer)
	tc.logger = zap.New(newJSONCore(tc.logs))
}

func (tc *TestCaseRequestContext) Run(ctx context.Context, t *testing.T) {
	runTest(ctx, t, tc.logs, func(ctx context.Context) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
		logWithRequest(req, tc.logger)
	})
}

func (*TestCaseRequestContext) ExpectedTraces() trace.Traces { return expectedTraces() }

// logWithRequest has an *http.Request but no context.Context parameter. The
// aspect uses req.Context() as the trace source.
func logWithRequest(req *http.Request, logger *zap.Logger) {
	logger.Info("handling request",
		zap.String("method", req.Method),
		zap.String("path", req.URL.Path),
	)
}
