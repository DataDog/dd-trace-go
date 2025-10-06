// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	streamprocessingoffload "github.com/DataDog/dd-trace-go/contrib/haproxy/stream-processing-offload/v2"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// Logger adapts the tracer instrumentation logger to the SPOE agent logger interface
type Logger struct {
	instrumentation.Logger
}

// NewLogger creates a new Logger instance
func NewLogger() *Logger {
	return &Logger{streamprocessingoffload.Logger()}
}

func (l Logger) Errorf(format string, args ...interface{}) {
	l.Error(format, args...)
}
