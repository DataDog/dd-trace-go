// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package instrumentation

import (
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

type logger struct {
	logOpts []telemetry.LogOption
}

func newLogger(pkg Package) *logger {
	return &logger{
		logOpts: []telemetry.LogOption{telemetry.WithTags([]string{"integration:" + string(pkg)})},
	}
}

func (l logger) Debug(msg string, args ...any) {
	log.Debug(msg, args...) //nolint:gocritic // Logger plumbing needs to pass through variable format strings
}

func (l logger) Info(msg string, args ...any) {
	log.Info(msg, args...) //nolint:gocritic // Logger plumbing needs to pass through variable format strings
}

func (l logger) Warn(msg string, args ...any) {
	log.Warn(msg, args...) //nolint:gocritic // Logger plumbing needs to pass through variable format strings
}

func (l logger) Error(msg string, args ...any) {
	log.Error(msg, args...) //nolint:gocritic // Logger plumbing needs to pass through variable format strings
}

func hasErrors(args ...any) bool {
	for _, arg := range args {
		if _, ok := arg.(error); ok {
			return true
		}
	}
	return false
}
