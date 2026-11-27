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

// TestCaseCorrelationAlias covers a logger literal when logrus is imported
// under another name.
type TestCaseCorrelationAlias struct {
	correlation
}

func (tc *TestCaseCorrelationAlias) Setup(context.Context, *testing.T) {
	out, formatter := tc.newLogs()
	tc.logger = &lr.Logger{
		Out:       out,
		Formatter: formatter,
		Hooks:     make(lr.LevelHooks),
		Level:     lr.DebugLevel,
		ExitFunc:  os.Exit,
	}
}
