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

// WithStacktrace returns a LogOption that requests a stack trace be attached
// to the telemetry log message. The stack is captured lazily by
// loggerBackend.add, at Log-dedup time — NOT inside this function — unless
// [Log] has already captured one synchronously because the global client
// wasn't installed yet (see [withRawStacktrace]). Logs demultiplication does
// not take the stacktrace into account: a log that has been demultiplicated
// will only show the first log's stack.
func WithStacktrace() LogOption {
	return func(_ *loggerKey, value *loggerValue) {
		if value == nil {
			return
		}
		value.captureStacktrace = true
	}
}

// withRawStacktrace attaches a stack trace captured earlier — before the
// global telemetry client existed — instead of leaving loggerBackend.add to
// capture one when the queued [Log] call is eventually replayed. Without
// this, the captured stack would belong to the replay goroutine, not the
// call site that actually requested it.
//
// Unexported: [Log] appends this automatically when it detects a queued
// [WithStacktrace] request; callers should keep using [WithStacktrace] only.
func withRawStacktrace(raw stacktrace.RawStackTrace) LogOption {
	return func(_ *loggerKey, value *loggerValue) {
		if value == nil {
			return
		}
		value.captureStacktrace = true
		value.rawStack = raw
	}
}

// wantsStacktrace reports whether options includes a [WithStacktrace]
// request, by applying each option's value-phase against a throwaway
// loggerValue — the same phase loggerBackend.add applies for real inside
// its LoadOrCompute closure.
func wantsStacktrace(options []LogOption) bool {
	var probe loggerValue
	for _, opt := range options {
		opt(nil, &probe)
	}
	return probe.captureStacktrace
}
