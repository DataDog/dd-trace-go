// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package zap

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// orchestrionEnabled returns whether orchestrion is enabled
func orchestrionEnabled() bool {
	return orchestrion.Enabled()
}

type TestCase struct {
	logger *zap.Logger
	logs   *bytes.Buffer
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	tc.logs = new(bytes.Buffer)

	// Debug: Check if orchestrion is enabled
	t.Logf("orchestrion.Enabled() = %v", orchestrionEnabled())

	// Create a custom encoder config for readable test output
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

	// zap.New is instrumented by orchestrion to wrap the core
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

	// Sync to flush any buffered log entries
	_ = tc.logger.Sync()

	logs := tc.logs.String()
	t.Logf("got logs: %s", logs)

	// Verify all log messages are present
	for _, msg := range []string{"debug", "info", "warn", "error"} {
		want := `"msg":"` + msg + `"`
		if !strings.Contains(logs, want) {
			t.Fatalf("missing log message %s", msg)
		}
	}

	// Note: Automatic trace context injection is not currently supported for zap
	// because zap's Write method does not receive a context.Context.
	// Use WithTraceFields(ctx, logger) for manual trace injection.
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
