// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"fmt"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry"
)

func divideArgs(args ...any) ([]newtelemetry.LogOption, []any) {
	if len(args) == 0 {
		return nil, nil
	}

	var options []newtelemetry.LogOption
	var fmtArgs []any
	for _, arg := range args {
		if opt, ok := arg.(newtelemetry.LogOption); ok {
			options = append(options, opt)
		} else {
			fmtArgs = append(fmtArgs, arg)
		}
	}
	return options, fmtArgs
}

// Debug sends a telemetry payload with a debug log message to the backend.
func Debug(format string, args ...any) {
	options, fmtArgs := divideArgs(args...)
	newtelemetry.Log(newtelemetry.LogDebug, fmt.Sprintf(format, fmtArgs...), options...)
}

// Warn sends a telemetry payload with a warning log message to the backend.
func Warn(format string, args ...any) {
	options, fmtArgs := divideArgs(args...)
	newtelemetry.Log(newtelemetry.LogWarn, fmt.Sprintf(format, fmtArgs...), options...)
}

// Error sends a telemetry payload with an error log message to the backend.
func Error(format string, args ...any) {
	options, fmtArgs := divideArgs(args...)
	newtelemetry.Log(newtelemetry.LogError, fmt.Sprintf(format, fmtArgs...), options...)
}
