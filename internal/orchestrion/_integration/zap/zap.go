// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package zap

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func orchestrionEnabled() bool {
	return orchestrion.Enabled()
}

type TestCase struct {
	logger *zap.Logger
	logs   *bytes.Buffer
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	tc.logs = new(bytes.Buffer)

	t.Logf("orchestrion.Enabled() = %v", orchestrionEnabled())

	encoderCfg := zapcore.EncoderConfig{
		MessageKey:     "msg",
		LevelKey:       "level",
		TimeKey:        "time",
		NameKey:        "logger",
		CallerKey:      "caller",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderCfg),
		zapcore.AddSync(tc.logs),
		zapcore.DebugLevel,
	)

	tc.logger = zap.New(core)

	// Debug: Log the core type to verify wrapping happened
	t.Logf("Created logger with core type: %T", core)
}

//dd:span
func Log(ctx context.Context, logger *zap.Logger, level zapcore.Level, msg string) {
	_ = ctx // ctx is used by orchestrion to inject trace context via GLS
	switch level {
	case zapcore.DebugLevel:
		logger.Debug(msg)
	case zapcore.InfoLevel:
		logger.Info(msg)
	case zapcore.WarnLevel:
		logger.Warn(msg)
	case zapcore.ErrorLevel:
		logger.Error(msg)
	}
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	Log(ctx, tc.logger, zapcore.DebugLevel, "debug")
	Log(ctx, tc.logger, zapcore.InfoLevel, "info")
	Log(ctx, tc.logger, zapcore.WarnLevel, "warn")
	Log(ctx, tc.logger, zapcore.ErrorLevel, "error")

	_ = tc.logger.Sync()

	logs := tc.logs.String()
	t.Logf("got logs: %s", logs)

	for _, msg := range []string{"debug", "info", "warn", "error"} {
		want := `"msg":"` + msg + `"`
		if !strings.Contains(logs, want) {
			t.Fatalf("missing log message %s", msg)
		}
	}

	// Verify trace context is injected into each log line via GLS
	lines := strings.Split(strings.TrimSpace(logs), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		t.Logf("checking line: %s", line)
		if ok, _ := regexp.MatchString(`"dd\.span_id":"\d+"`, line); !ok {
			t.Errorf("no span ID in log line: %s", line)
		}
		if ok, _ := regexp.MatchString(`"dd\.trace_id":"[0-9a-f]+"`, line); !ok {
			t.Errorf("no trace ID in log line: %s", line)
		}
	}
}

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "Log",
			},
		},
		{
			Tags: map[string]any{
				"name": "Log",
			},
		},
		{
			Tags: map[string]any{
				"name": "Log",
			},
		},
		{
			Tags: map[string]any{
				"name": "Log",
			},
		},
	}
}
