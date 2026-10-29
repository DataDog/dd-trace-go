// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package logrusdep builds logrus loggers inside a dependency module.
package logrusdep

import (
	"io"
	"os"

	"github.com/sirupsen/logrus"
)

// NewLogger returns a logger built from a &logrus.Logger{} literal.
func NewLogger(out io.Writer, formatter logrus.Formatter) *logrus.Logger {
	return &logrus.Logger{
		Out:       out,
		Formatter: formatter,
		Hooks:     make(logrus.LevelHooks),
		Level:     logrus.DebugLevel,
		ExitFunc:  os.Exit,
	}
}

// NewValueLogger returns a logger built from a logrus.Logger{} literal.
func NewValueLogger(out io.Writer, formatter logrus.Formatter) logrus.Logger {
	return logrus.Logger{
		Out:       out,
		Formatter: formatter,
		Hooks:     make(logrus.LevelHooks),
		Level:     logrus.DebugLevel,
		ExitFunc:  os.Exit,
	}
}
