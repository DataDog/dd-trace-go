// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package logrus

import (
	"context"
	"os"
	"testing"

	lr "github.com/sirupsen/logrus"
)

// TestCaseCorrelationShadowed covers a logger literal in a scope that declares
// logrus and logrustrace, in a file that imports logrus under another name. The
// instrumentation must not resolve to those locals.
type TestCaseCorrelationShadowed struct {
	correlation
}

func (tc *TestCaseCorrelationShadowed) Setup(context.Context, *testing.T) {
	out, formatter := tc.newLogs()
	logrustrace := lr.DebugLevel
	logrus := &lr.Logger{
		Out:       out,
		Formatter: formatter,
		Hooks:     make(lr.LevelHooks),
		Level:     logrustrace,
		ExitFunc:  os.Exit,
	}
	tc.logger = logrus
}
