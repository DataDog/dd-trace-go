// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"log/slog"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// ReportError forwards a constant-message SDK error to telemetry.
//
// msg MUST be a constant string — never the result of fmt.Sprintf, string
// concatenation, or err.Error(). The constant message is used as a dedup key
// in telemetry; non-constant values break deduplication and risk leaking PII.
//
// err is scrubbed through [NewSafeError] before transmission, so only the
// error type (not the message) is sent to telemetry.
//
// opts may include [telemetry.WithTags] or additional options. A redacted
// stack trace is always attached.
func ReportError(msg string, err error, opts ...telemetry.LogOption) {
	action := lookupPolicy(msg)
	if action == policyExclude {
		return
	}

	level := telemetry.LogError
	if action == policyDowngrade {
		level = telemetry.LogWarn
	}

	record := telemetry.NewRecord(level, msg)
	if err != nil {
		record.AddAttrs(slog.Any("error", NewSafeError(err)))
	}

	allOpts := make([]telemetry.LogOption, 0, len(opts)+1)
	allOpts = append(allOpts, telemetry.WithStacktrace())
	allOpts = append(allOpts, opts...)
	sendLog(record, allOpts...)
}

// ReportPanic forwards a recovered panic to telemetry as an error.
//
// msg MUST be a constant string — see [ReportError] for the rationale.
//
// recovered is the value returned by recover(). If it implements error,
// it is scrubbed through [NewSafeError] before transmission.
// A redacted stack trace is always attached.
func ReportPanic(recovered any, msg string) {
	action := lookupPolicy(msg)
	if action == policyExclude {
		return
	}

	record := telemetry.NewRecord(telemetry.LogError, msg)
	if recovered != nil {
		if err, ok := recovered.(error); ok {
			record.AddAttrs(slog.Any("error", NewSafeError(err)))
		}
	}

	sendLog(record, telemetry.WithStacktrace())
}
