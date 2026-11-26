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

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseEvaluation covers a receiver and arguments with side effects. Each
// must be evaluated once, the receiver first.
type TestCaseEvaluation struct {
	logger *zap.Logger
	logs   *bytes.Buffer
	calls  []string
}

func (tc *TestCaseEvaluation) Setup(context.Context, *testing.T) {
	tc.logs = new(bytes.Buffer)
	tc.logger = zap.New(newJSONCore(tc.logs))
}

func (tc *TestCaseEvaluation) Run(ctx context.Context, t *testing.T) {
	runTestExact(ctx, t, tc.logs, 2, func(ctx context.Context) {
		tc.logWithSideEffects(ctx)
	})
	assert.Equal(t, []string{"logger", "field", "sugar", "arg"}, tc.calls)
}

func (*TestCaseEvaluation) ExpectedTraces() trace.Traces { return expectedTraces() }

func (tc *TestCaseEvaluation) logWithSideEffects(ctx context.Context) {
	tc.getLogger().Info("side effects", tc.field())
	tc.getSugar().Infow("side effects sugared", "k", tc.arg())
}

func (tc *TestCaseEvaluation) getLogger() *zap.Logger {
	tc.calls = append(tc.calls, "logger")
	return tc.logger
}

func (tc *TestCaseEvaluation) getSugar() *zap.SugaredLogger {
	tc.calls = append(tc.calls, "sugar")
	return tc.logger.Sugar()
}

func (tc *TestCaseEvaluation) field() zap.Field {
	tc.calls = append(tc.calls, "field")
	return zap.String("k", "v")
}

func (tc *TestCaseEvaluation) arg() string {
	tc.calls = append(tc.calls, "arg")
	return "v"
}
