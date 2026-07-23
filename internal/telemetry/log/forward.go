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
	internallog.SetWarnTelemetrySink(forwardWarn)
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
	attachFirstError(&record, args)
	sendLog(record, telemetry.WithStacktrace())
}

// forwardWarn is the telemetry sink installed on internal/log.Warn. Unlike
// forwardError, forwarding is opt-in per template: only formats explicitly
// marked policyReport in the policy table are forwarded — an absent or
// excluded/downgraded entry means "stay local-only". It must not call
// log.Warn or log.Error itself.
func forwardWarn(format string, args []any) {
	if !warnOptedIn(format) {
		return
	}

	record := telemetry.NewRecord(telemetry.LogWarn, format)
	attachFirstError(&record, args)
	sendLog(record, telemetry.WithStacktrace())
}

// attachFirstError attaches the first error argument found in args, scrubbed
// through NewSafeError. Other argument types are never attached (PII risk).
func attachFirstError(record *telemetry.Record, args []any) {
	for _, arg := range args {
		if err, ok := arg.(error); ok {
			record.AddAttrs(slog.Any("error", NewSafeError(err)))
			return // only attach the first error argument
		}
	}
}
