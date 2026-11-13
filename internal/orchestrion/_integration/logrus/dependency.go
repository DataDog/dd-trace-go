// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package logrus

import (
	"context"
	"testing"

	"example.com/logrusdep"
	"github.com/sirupsen/logrus"
)

// TestCaseDependencyLiteralPtr covers a &logrus.Logger{} literal in a
// dependency module, not in the module being built.
type TestCaseDependencyLiteralPtr struct {
	correlation
}

func (tc *TestCaseDependencyLiteralPtr) Setup(context.Context, *testing.T) {
	tc.logger = logrusdep.NewLogger(tc.newLogs())
}

// TestCaseDependencyLiteralVal covers a logrus.Logger{} literal in a
// dependency module.
type TestCaseDependencyLiteralVal struct {
	correlation
	val logrus.Logger
}

func (tc *TestCaseDependencyLiteralVal) Setup(context.Context, *testing.T) {
	tc.val = logrusdep.NewValueLogger(tc.newLogs())
	tc.logger = &tc.val
}
