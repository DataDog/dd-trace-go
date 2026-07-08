// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"log/slog"

	internallog "github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

func init() {
	internallog.SetErrorTelemetrySink(forwardError)
}

// forwardError is the telemetry sink installed on internal/log.Error.
// It is called with the raw format string (used as the constant dedup key)
// and the original variadic arguments. It must not call log.Error itself.
func forwardError(format string, args []any) {
	action := lookupPolicy(format)
	if action == policyExclude {
		return
	}

	level := telemetry.LogError
	if action == policyDowngrade {
		level = telemetry.LogWarn
	}

	record := telemetry.NewRecord(level, format)
	for _, arg := range args {
		if err, ok := arg.(error); ok {
			record.AddAttrs(slog.Any("error", NewSafeError(err)))
			break // only attach the first error argument
		}
	}

	sendLog(record, telemetry.WithStacktrace())
}
