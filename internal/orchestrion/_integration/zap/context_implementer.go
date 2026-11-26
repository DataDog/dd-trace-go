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

// TestCaseContextImplementer covers a calling function whose parameter is not
// a context.Context but implements it.
type TestCaseContextImplementer struct {
	logger *zap.Logger
	logs   *bytes.Buffer
}

func (tc *TestCaseContextImplementer) Setup(context.Context, *testing.T) {
	tc.logs = new(bytes.Buffer)
	tc.logger = zap.New(newJSONCore(tc.logs))
}

func (tc *TestCaseContextImplementer) Run(ctx context.Context, t *testing.T) {
	runTestExact(ctx, t, tc.logs, 2, func(ctx context.Context) {
		logWithContextImplementer(requestContext{ctx}, tc.logger)
	})
}

func (*TestCaseContextImplementer) ExpectedTraces() trace.Traces { return expectedTraces() }

// requestContext stands for a framework context type, such as *gin.Context,
// that implements context.Context.
type requestContext struct {
	context.Context
}

func logWithContextImplementer(c requestContext, logger *zap.Logger) {
	logger.Info("context implementer")
	logger.Sugar().Infof("context implementer %s", "sugared")
}
