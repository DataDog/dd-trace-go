// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package zap

// TestCaseNilRequest verifies that logging does not panic when the calling
// function has a *http.Request parameter but it is nil. The aspect falls back
// to TraceFieldsFromRequest, which is nil-safe, so trace fields are simply
// omitted rather than panicking on req.Context().

import (
	"bufio"
	"bytes"
	"context"
	"net/http"
	"regexp"
	"testing"

	"go.uber.org/zap"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseNilRequest struct {
	logger *zap.Logger
	logs   *bytes.Buffer
}

func (tc *TestCaseNilRequest) Setup(_ context.Context, _ *testing.T) {
	tc.logs = new(bytes.Buffer)
	tc.logger = zap.New(newJSONCore(tc.logs))
}

func (tc *TestCaseNilRequest) Run(ctx context.Context, t *testing.T) {
	span, _ := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	// Must not panic even though req is nil.
	logWithNilRequest(nil, tc.logger)

	logs := tc.logs.String()
	t.Logf("got logs:\n%s", logs)

	s := bufio.NewScanner(bytes.NewBufferString(logs))
	for s.Scan() {
		line := s.Bytes()
		if ok, _ := regexp.Match(`"dd\.trace_id"`, line); ok {
			t.Errorf("unexpected dd.trace_id injected in log line (nil request): %s", line)
		}
		if ok, _ := regexp.Match(`"dd\.span_id"`, line); ok {
			t.Errorf("unexpected dd.span_id injected in log line (nil request): %s", line)
		}
	}
}

func (*TestCaseNilRequest) ExpectedTraces() trace.Traces { return expectedTraces() }

// logWithNilRequest has a *http.Request but no context.Context parameter. The
// aspect must not panic when req is nil.
func logWithNilRequest(req *http.Request, logger *zap.Logger) {
	logger.Info("handling request with nil request")
}
