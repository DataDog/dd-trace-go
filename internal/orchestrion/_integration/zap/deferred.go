// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package zap

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDeferred covers deferred log calls. Their arguments must still be
// evaluated at the defer statement, not when the function returns.
type TestCaseDeferred struct {
	logger *zap.Logger
	logs   *bytes.Buffer
}

func (tc *TestCaseDeferred) Setup(context.Context, *testing.T) {
	tc.logs = new(bytes.Buffer)
	tc.logger = zap.New(newJSONCore(tc.logs))
}

func (tc *TestCaseDeferred) Run(ctx context.Context, t *testing.T) {
	runTestExact(ctx, t, tc.logs, 2, func(ctx context.Context) {
		logDeferred(ctx, tc.logger)
	})
	for line := range strings.SplitSeq(strings.TrimSpace(tc.logs.String()), "\n") {
		var data map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &data))
		assert.EqualValues(t, 1, data["step"], "argument not evaluated at the defer statement: %s", line)
	}
}

func (*TestCaseDeferred) ExpectedTraces() trace.Traces { return expectedTraces() }

func logDeferred(ctx context.Context, logger *zap.Logger) {
	step := 1
	defer logger.Info("deferred", zap.Int("step", step))
	defer logger.Sugar().Infow("deferred sugared", "step", step)
	step = 2
	_ = step
}
