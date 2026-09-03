// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"fmt"
	"log/slog"

	internallog "github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// ReportError forwards a constant-message SDK error to telemetry. Call this
// explicitly at any site that wants its error surfaced in Error Tracking —
// there is no automatic forwarding from internal/log.Error; a site that
// doesn't call ReportError simply isn't reported.
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
	record := telemetry.NewRecord(telemetry.LogError, msg)
	if err != nil {
		record.AddAttrs(slog.Any("error", NewSafeError(err)))
	}

	allOpts := make([]telemetry.LogOption, 0, len(opts)+1)
	allOpts = append(allOpts, telemetry.WithStacktraceNow())
	allOpts = append(allOpts, opts...)
	sendLog(record, allOpts...)
}

// ReportPanic forwards a recovered panic to telemetry as an error. Call this
// explicitly at any recover() site that wants the panic surfaced in Error
// Tracking — see [ReportError] for why this is opt-in per call site.
//
// msg MUST be a constant string — see [ReportError] for the rationale.
//
// recovered is the value returned by recover(). If it implements error, it is
// scrubbed through [NewSafeError] before transmission. Otherwise (e.g. a
// panic(string) or a plain struct), only its type is attached — never its
// content — matching the same disclosure rule [NewSafeError] applies to errors.
// A redacted stack trace is always attached.
//
// opts may include [telemetry.WithTags] or additional options.
func ReportPanic(msg string, recovered any, opts ...telemetry.LogOption) {
	record := telemetry.NewRecord(telemetry.LogError, msg)
	if recovered != nil {
		if err, ok := recovered.(error); ok {
			record.AddAttrs(slog.Any("error", NewSafeError(err)))
		} else {
			record.AddAttrs(slog.String("recovered_type", errorType(recovered)))
		}
	}

	allOpts := make([]telemetry.LogOption, 0, len(opts)+1)
	allOpts = append(allOpts, telemetry.WithStacktraceNow())
	allOpts = append(allOpts, opts...)
	sendLog(record, allOpts...)
}

// LogAndReportError logs msg locally via internal/log.Error and forwards the
// same constant msg to Error Tracking via [ReportError] — for call sites that
// want both a local log line and a report without duplicating the message.
//
// msg MUST be a constant string — see [ReportError] for the rationale. The
// format string passed to internal/log.Error is always msg+": %s" regardless
// of whether err is nil, so the call site's local dedup key stays stable.
func LogAndReportError(msg string, err error, opts ...telemetry.LogOption) {
	errStr := "<nil>"
	if err != nil {
		// fmt.Sprint recovers a panicking Error() method (e.g. a typed-nil
		// receiver that dereferences itself) instead of letting it escape.
		errStr = fmt.Sprint(err)
	}
	internallog.Error(msg+": %s", errStr)
	ReportError(msg, err, opts...)
}

// LogAndReportPanic mirrors [LogAndReportError] for recover() sites, pairing
// with [ReportPanic].
func LogAndReportPanic(msg string, recovered any, opts ...telemetry.LogOption) {
	errStr := "<nil>"
	if recovered != nil {
		// fmt.Sprint recovers a panicking Error() method (e.g. a typed-nil
		// receiver that dereferences itself) instead of letting it escape.
		errStr = fmt.Sprint(recovered)
	}
	internallog.Error(msg+": %s", errStr)
	ReportPanic(msg, recovered, opts...)
}
