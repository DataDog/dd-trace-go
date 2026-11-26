// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package zap

import (
	"bytes"
	"context"
	"testing"

	uzap "go.uber.org/zap"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport covers zap calls in a file that imports zap under
// another name.
type TestCaseAliasedImport struct {
	logger *uzap.Logger
	logs   *bytes.Buffer
}

func (tc *TestCaseAliasedImport) Setup(context.Context, *testing.T) {
	tc.logs = new(bytes.Buffer)
	tc.logger = uzap.New(newJSONCore(tc.logs))
}

func (tc *TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	runTestExact(ctx, t, tc.logs, 2, func(ctx context.Context) {
		logWithAliasedImport(ctx, tc.logger)
	})
}

func (*TestCaseAliasedImport) ExpectedTraces() trace.Traces { return expectedTraces() }

func logWithAliasedImport(ctx context.Context, logger *uzap.Logger) {
	logger.Info("aliased import", uzap.String("k", "v"))
	logger.Sugar().Infow("aliased import sugared", "k", "v")
}
