// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package logrus

import (
	"bytes"
	"context"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseGlobalLogger struct {
	logs *bytes.Buffer
}

func (tc *TestCaseGlobalLogger) Setup(context.Context, *testing.T) {
	tc.logs = new(bytes.Buffer)
	logrus.SetLevel(logrus.DebugLevel)
	logrus.SetOutput(tc.logs)
}

func (tc *TestCaseGlobalLogger) Run(ctx context.Context, t *testing.T) {
	runTest(ctx, t, tc.logs, tc.Log)
}

func (*TestCaseGlobalLogger) ExpectedTraces() trace.Traces {
	return expectedTraces()
}

//dd:span
func (*TestCaseGlobalLogger) Log(ctx context.Context, level logrus.Level, msg string) {
	logrus.WithContext(ctx).Log(level, msg)
}
