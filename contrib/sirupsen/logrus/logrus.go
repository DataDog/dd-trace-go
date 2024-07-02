// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package logrus provides a log/span correlation hook for the sirupsen/logrus package (https://github.com/sirupsen/logrus).
package logrus

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/sirupsen/logrus"
)

const componentName = "sirupsen/logrus"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/sirupsen/logrus")
}

// DDContextLogHook ensures that any span in the log context is correlated to log output.
type DDContextLogHook struct{}

// Levels implements logrus.Hook interface, this hook applies to all defined levels
func (d *DDContextLogHook) Levels() []logrus.Level {
	return []logrus.Level{logrus.PanicLevel, logrus.FatalLevel, logrus.ErrorLevel, logrus.WarnLevel, logrus.InfoLevel, logrus.DebugLevel, logrus.TraceLevel}
}

// Fire implements logrus.Hook interface, attaches trace and span details found in entry context
func (d *DDContextLogHook) Fire(e *logrus.Entry) error {
	span, found := tracer.SpanFromContext(e.Context)
	if !found {
		return nil
	}
	e.Data[ext.LogKeyTraceID] = span.Context().TraceID()
	e.Data[ext.LogKeySpanID] = span.Context().SpanID()
	return nil
}
