// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package profiler

import (
	"fmt"
	"log"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"
)

var prefixMsg = fmt.Sprintf("Datadog Tracer %s", version.Tag)

type logger struct {
	l ddtrace.Logger
}

func newLogger(l ddtrace.Logger) *logger {
	return &logger{l: l}
}

func (l *logger) Info(format string, a ...interface{})  { l.msg("INFO", format, a...) }
func (l *logger) Error(format string, a ...interface{}) { l.msg("ERROR", format, a...) }
func (l *logger) Warn(format string, a ...interface{})  { l.msg("WARN", format, a...) }

func (l *logger) msg(lvl, format string, a ...interface{}) {
	if l == nil {
		return
	}
	msg := fmt.Sprintf("%s %s: %s", prefixMsg, lvl, fmt.Sprintf(format, a...))
	l.l.Log(msg)
}

type stdLogger struct{}

func (stdLogger) Log(msg string) { log.Println(msg) }

var defaultLogger = newLogger(stdLogger{})
