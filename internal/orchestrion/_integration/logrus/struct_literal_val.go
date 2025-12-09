// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package logrus

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseStructLiteralVal struct {
	logger logrus.Logger
	logs   *bytes.Buffer
}

func (tc *TestCaseStructLiteralVal) Setup(context.Context, *testing.T) {
	tc.logs = new(bytes.Buffer)
	tc.logger = logrus.Logger{
		Out:          os.Stderr,
		Formatter:    new(logrus.TextFormatter),
		Hooks:        make(logrus.LevelHooks),
		Level:        logrus.InfoLevel,
		ExitFunc:     os.Exit,
		ReportCaller: false,
	}
	tc.logger.SetLevel(logrus.DebugLevel)
	tc.logger.SetOutput(tc.logs)
}

func (tc *TestCaseStructLiteralVal) Run(ctx context.Context, t *testing.T) {
	runTest(ctx, t, tc.logs, tc.Log)
}

func (*TestCaseStructLiteralVal) ExpectedTraces() trace.Traces {
	return expectedTraces()
}

//dd:span
func (tc *TestCaseStructLiteralVal) Log(ctx context.Context, level logrus.Level, msg string) {
	tc.logger.WithContext(ctx).Log(level, msg)
}
