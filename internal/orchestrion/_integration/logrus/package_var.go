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
)

// pkgLogger is built during package initialization, before the tracer starts.
var pkgLogger = &logrus.Logger{
	Hooks:    make(logrus.LevelHooks),
	ExitFunc: os.Exit,
}

// TestCaseCorrelationPackageVar covers a logger literal in a package-level
// variable.
type TestCaseCorrelationPackageVar struct {
	correlation
}

func (tc *TestCaseCorrelationPackageVar) Setup(context.Context, *testing.T) {
	out, formatter := tc.newLogs()
	pkgLogger.SetLevel(logrus.DebugLevel)
	pkgLogger.SetOutput(out)
	pkgLogger.SetFormatter(formatter)
	tc.logger = pkgLogger
}
