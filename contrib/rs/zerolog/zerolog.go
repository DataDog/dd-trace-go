// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package zerolog provides a log/span correlation hook for the sirupsen/zerolog package (https://github.com/sirupsen/zerolog).
package zerolog

import (
	"strconv"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
	"github.com/rs/zerolog"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageRsZerolog)
}

// DDContextLogHook ensures that any span in the log context is correlated to log output.
type DDContextLogHook struct{}

var _ zerolog.Hook = DDContextLogHook{}

type config struct {
	log128bits bool
}

var cfg = newConfig()

func newConfig() *config {
	return &config{
		log128bits: options.GetBoolEnv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", true),
	}
}

// Run implements zerolog.Hook interface, attaches trace and span details found in entry context
func (d DDContextLogHook) Run(e *zerolog.Event, _ zerolog.Level, _ string) {
	span, found := tracer.SpanFromContext(e.GetCtx())
	if !found {
		return
	}
	if cfg.log128bits && span.Context().TraceID() != tracer.TraceIDZero {
		e.Str(ext.LogKeyTraceID, span.Context().TraceID())
	} else {
		e.Str(ext.LogKeyTraceID, strconv.FormatUint(span.Context().TraceIDLower(), 10))
	}
	e.Str(ext.LogKeySpanID, strconv.FormatUint(span.Context().SpanID(), 10))
}
