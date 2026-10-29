// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package logrus

import (
	"context"
	"os"
	"testing"

	"github.com/sirupsen/logrus"

	logrustrace "github.com/DataDog/dd-trace-go/contrib/sirupsen/logrus/v2"

	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestCaseCorrelationNew covers a logger built with logrus.New.
type TestCaseCorrelationNew struct {
	correlation
}

func (tc *TestCaseCorrelationNew) Setup(context.Context, *testing.T) {
	out, formatter := tc.newLogs()
	tc.logger = logrus.New()
	tc.logger.SetLevel(logrus.DebugLevel)
	tc.logger.SetOutput(out)
	tc.logger.SetFormatter(formatter)
}

// TestCaseCorrelationStandard covers the standard logger behind the
// package-level functions, which logrus builds in its own package.
type TestCaseCorrelationStandard struct {
	correlation
}

func (tc *TestCaseCorrelationStandard) Setup(_ context.Context, t *testing.T) {
	out, formatter := tc.newLogs()
	tc.logger = logrus.StandardLogger()
	prevLevel, prevOut, prevFormatter := logrus.GetLevel(), tc.logger.Out, tc.logger.Formatter
	t.Cleanup(func() {
		logrus.SetLevel(prevLevel)
		logrus.SetOutput(prevOut)
		logrus.SetFormatter(prevFormatter)
	})
	logrus.SetLevel(logrus.DebugLevel)
	logrus.SetOutput(out)
	logrus.SetFormatter(formatter)
}

// TestCaseCorrelationLiteralPtr covers a &logrus.Logger{} literal.
type TestCaseCorrelationLiteralPtr struct {
	correlation
}

func (tc *TestCaseCorrelationLiteralPtr) Setup(context.Context, *testing.T) {
	out, formatter := tc.newLogs()
	tc.logger = &logrus.Logger{
		Out:       out,
		Formatter: formatter,
		Hooks:     make(logrus.LevelHooks),
		Level:     logrus.DebugLevel,
		ExitFunc:  os.Exit,
	}
}

// TestCaseCorrelationLiteralVal covers a logrus.Logger{} literal.
type TestCaseCorrelationLiteralVal struct {
	correlation
	val logrus.Logger
}

func (tc *TestCaseCorrelationLiteralVal) Setup(context.Context, *testing.T) {
	out, formatter := tc.newLogs()
	tc.val = logrus.Logger{
		Out:       out,
		Formatter: formatter,
		Hooks:     make(logrus.LevelHooks),
		Level:     logrus.DebugLevel,
		ExitFunc:  os.Exit,
	}
	tc.logger = &tc.val
}

// TestCaseCorrelationLiteralNoHooks covers a literal that leaves Hooks nil.
// Orchestrion adds its hook to the nil map and panics.
type TestCaseCorrelationLiteralNoHooks struct {
	correlation
}

func (tc *TestCaseCorrelationLiteralNoHooks) Setup(_ context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion panics on a logrus.Logger literal without Hooks")
	}
	out, formatter := tc.newLogs()
	tc.logger = &logrus.Logger{
		Out:       out,
		Formatter: formatter,
		Level:     logrus.DebugLevel,
		ExitFunc:  os.Exit,
	}
}

// TestCaseCorrelationContribHook covers loggers the application also sets up
// with the contrib's hook, which must still fire once per entry. Orchestrion
// injects its own hook type and registers both.
type TestCaseCorrelationContribHook struct {
	correlation
	loggers []*logrus.Logger
}

func (tc *TestCaseCorrelationContribHook) Setup(_ context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion registers its hook next to the contrib's")
	}
	out, formatter := tc.newLogs()

	newLogger := logrus.New()
	newLogger.AddHook(&logrustrace.DDContextLogHook{})

	hooks := make(logrus.LevelHooks)
	hooks.Add(&logrustrace.DDContextLogHook{})
	literal := &logrus.Logger{Hooks: hooks}

	tc.loggers = []*logrus.Logger{newLogger, literal}
	for _, l := range tc.loggers {
		l.SetLevel(logrus.DebugLevel)
		l.SetOutput(out)
		l.SetFormatter(formatter)
	}
	tc.logger = newLogger
}

func (tc *TestCaseCorrelationContribHook) Run(ctx context.Context, t *testing.T) {
	requireOneContextLogHook(t, tc.loggers[1])
	tc.correlation.Run(ctx, t)
}
