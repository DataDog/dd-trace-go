// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package logrus provides a log/span correlation hook for the sirupsen/logrus package (https://github.com/sirupsen/logrus).
package logrus

import (
	v2 "github.com/DataDog/dd-trace-go/v2/contrib/sirupsen/logrus"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/sirupsen/logrus"
)

// DDContextLogHook ensures that any span in the log context is correlated to log output.
type DDContextLogHook struct {
	v2.DDContextLogHook
}

// Fire implements logrus.Hook interface, attaches trace and span details found in entry context
func (d *DDContextLogHook) Fire(e *logrus.Entry) error {
	span, found := tracer.SpanFromContext(e.Context)
	if !found {
		return nil
	}
	d.DDContextLogHook.Fire(e)
	// To keep v1 behavior, we set the trace_id as 64-bit integer.
	// This is not the default behavior in v2.
	e.Data["dd.trace_id"] = span.Context().TraceID()
	return nil
}
