// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetry

import (
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/stacktrace"
)

// WithTags returns a LogOption that appends the tags for the telemetry log message. Tags are key-value pairs that are then
// serialized into a simple "key:value,key2:value2" format. No quoting or escaping is performed.
// Multiple calls to WithTags will append tags without duplications, preserving the order of first occurrence.
func WithTags(tags []string) LogOption {
	if len(tags) == 0 {
		return func(*loggerKey, *loggerValue) {}
	}

	// Pre-compute joined string to minimize closure size (string vs slice header)
	// and avoid repeated joins in the common fast-path case
	compiled := strings.Join(tags, ",")

	return func(key *loggerKey, _ *loggerValue) {
		if key == nil {
			return
		}

		if key.tags == "" {
			// Fast path: no existing tags, just assign
			key.tags = compiled
			return
		}

		// Slow path: merge and deduplicate
		seen := make(map[string]struct{})

		var builder strings.Builder
		builder.Grow(len(key.tags) + len(compiled) + 1)

		// Add existing tags
		for tag := range strings.SplitSeq(key.tags, ",") {
			if builder.Len() > 0 {
				builder.WriteByte(',')
			}
			builder.WriteString(tag)
			seen[tag] = struct{}{}
		}

		// Add new tags, skipping duplicates
		for tag := range strings.SplitSeq(compiled, ",") {
			if _, exists := seen[tag]; !exists {
				if builder.Len() > 0 {
					builder.WriteByte(',')
				}
				builder.WriteString(tag)
				seen[tag] = struct{}{}
			}
		}

		key.tags = builder.String()
	}
}

// WithStacktrace returns a LogOption that sets the stacktrace for the telemetry log message. The stacktrace is a string
// that is generated inside the WithStacktrace function. Logs demultiplication does not take the stacktrace into account.
// This means that a log that has been demultiplicated will only show of the first log.
func WithStacktrace() LogOption {
	return func(_ *loggerKey, value *loggerValue) {
		if value == nil {
			return
		}
		value.captureStacktrace = true
	}
}

// withStacktraceNowSkip skips WithStacktraceNow's own frame, landing the
// capture on whatever function called it (e.g. ReportError) — the same
// "keep telemetry call-chain frames, only skip pure capture machinery"
// convention as telemetryStackSkip in backend.go.
const withStacktraceNowSkip = 1

// WithStacktraceNow returns a LogOption that captures the stack trace
// synchronously, at the caller's own call site, right now — instead of
// deferring capture to whenever the backend actually processes the record
// (see [WithStacktrace]). Use this at any call site whose Log call may be
// queued and replayed later (e.g. before telemetry.StartApp runs), so the
// stack reflects where the call was actually made, not the replay machinery
// that eventually delivers it.
//
// When telemetry is disabled ([Disabled] is true), this returns the same
// no-capture-yet option as WithStacktrace instead of paying the capture cost:
// every call through the package-level Log function is a no-op in that case,
// so there's nothing to preserve accuracy for.
//
// Entries sent with this option are deduplicated separately from entries
// without it: the backend's dedup key carries a flag set here, so a report
// can never merge into a plain, stackless log entry that happens to share
// the same message, level, and tags — a merge would drop the report's
// stack trace and error attributes.
func WithStacktraceNow() LogOption {
	if Disabled() {
		return WithStacktrace()
	}
	raw := stacktrace.CaptureRaw(withStacktraceNowSkip)
	return func(key *loggerKey, value *loggerValue) {
		if key != nil {
			key.stackNow = true
			return
		}
		if value == nil {
			return
		}
		value.captureStacktrace = true
		value.stacktraceCaptured = true
		value.rawStack = raw
	}
}
