// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package slog

import (
	"bufio"
	"bytes"
	"context"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCase struct {
	logger *slog.Logger
	logs   *bytes.Buffer
}

func (tc *TestCase) Setup(context.Context, *testing.T) {
	tc.logs = new(bytes.Buffer)
	handler := slog.NewTextHandler(
		tc.logs,
		&slog.HandlerOptions{Level: slog.LevelDebug},
	)
	tc.logger = slog.New(handler)
	// call slog.New again to trigger possible errors in the aspect
	slog.New(handler)
}

//dd:span
func Log(ctx context.Context, f func(context.Context, string, ...any), msg string) {
	f(ctx, msg)
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	Log(ctx, tc.logger.DebugContext, "debug")
	Log(ctx, tc.logger.InfoContext, "info")
	Log(ctx, tc.logger.WarnContext, "warn")
	Log(ctx, tc.logger.ErrorContext, "error")
	Log(ctx, func(ctx context.Context, s string, a ...any) {
		tc.logger.Log(ctx, slog.LevelInfo, s, a...)
	}, "log")

	logs := tc.logs.String()
	t.Logf("got logs: %s", logs)
	for _, msg := range []string{"debug", "info", "warn", "error", "log"} {
		want := "msg=" + msg
		if !strings.Contains(logs, want) {
			t.Fatalf("missing log message %s", msg)
		}
	}

	s := bufio.NewScanner(tc.logs)
	for s.Scan() {
		line := s.Bytes()
		t.Logf("%s", line)
		if ok, _ := regexp.Match(`dd.span_id=\d+`, line); !ok {
			t.Errorf("no span ID")
		}
		if ok, _ := regexp.Match(`dd.trace_id=\d+`, line); !ok {
			t.Errorf("no trace ID")
		}
	}
}

func (*TestCase) ExpectedTraces() trace.Traces { return trace.Traces{} }
