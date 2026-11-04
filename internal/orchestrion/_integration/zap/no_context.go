// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package zap

// TestCaseNoContext verifies that trace fields are NOT injected when no
// context.Context is available in the calling function's scope.

import (
	"bufio"
	"bytes"
	"context"
	"regexp"
	"testing"

	"go.uber.org/zap"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseNoContext struct {
	logger *zap.Logger
	logs   *bytes.Buffer
}

func (tc *TestCaseNoContext) Setup(_ context.Context, _ *testing.T) {
	tc.logs = new(bytes.Buffer)
	tc.logger = zap.New(newJSONCore(tc.logs))
}

func (tc *TestCaseNoContext) Run(ctx context.Context, t *testing.T) {
	// A span is active, but ctx is intentionally not passed to logWithoutContext.
	span, _ := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	logWithoutContext(tc.logger)

	logs := tc.logs.String()
	t.Logf("got logs:\n%s", logs)

	s := bufio.NewScanner(bytes.NewBufferString(logs))
	for s.Scan() {
		line := s.Bytes()
		if ok, _ := regexp.Match(`"dd\.trace_id"`, line); ok {
			t.Errorf("unexpected dd.trace_id injected in log line (no context in scope): %s", line)
		}
		if ok, _ := regexp.Match(`"dd\.span_id"`, line); ok {
			t.Errorf("unexpected dd.span_id injected in log line (no context in scope): %s", line)
		}
	}
}

func (*TestCaseNoContext) ExpectedTraces() trace.Traces { return expectedTraces() }

// logWithoutContext has no context.Context parameter — no trace fields should
// be injected into the log output.
func logWithoutContext(logger *zap.Logger) {
	logger.Info("no context available")
}
