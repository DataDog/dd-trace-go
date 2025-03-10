// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package instrumentation

import (
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	telemetrylog "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"
)

type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

type logger struct{}

func (l logger) Debug(msg string, args ...any) {
	log.Debug(msg, args...)
	telemetrylog.Debug(msg, args...)
}

func (l logger) Info(msg string, args ...any) {
	log.Info(msg, args...)
}

func (l logger) Warn(msg string, args ...any) {
	log.Warn(msg, args...)
	telemetrylog.Warn(msg, args...)
}

func (l logger) Error(msg string, args ...any) {
	log.Error(msg, args...)
	telemetrylog.Error(msg, args...)
}
