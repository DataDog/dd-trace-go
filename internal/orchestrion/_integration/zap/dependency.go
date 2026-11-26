// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package zap

import (
	"bytes"
	"context"
	"testing"

	"example.com/zapdep"
	"go.uber.org/zap"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency covers zap calls in a dependency module, not in the
// module being built.
type TestCaseDependency struct {
	logger *zap.Logger
	logs   *bytes.Buffer
}

func (tc *TestCaseDependency) Setup(context.Context, *testing.T) {
	tc.logs = new(bytes.Buffer)
	tc.logger = zap.New(newJSONCore(tc.logs))
}

func (tc *TestCaseDependency) Run(ctx context.Context, t *testing.T) {
	runTestExact(ctx, t, tc.logs, 2, func(ctx context.Context) {
		zapdep.Log(ctx, tc.logger, "from dependency")
	})
}

func (*TestCaseDependency) ExpectedTraces() trace.Traces { return expectedTraces() }
