// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetry

import (
	"strings"
)

// estimatedAvgTagLen is a heuristic for pre-allocating string builder capacity.
// Typical tags like "product:appsec" (14 chars) or "env:production" (14 chars).
// Under-estimating causes additional allocations when the builder grows.
// Over-estimating wastes memory until the final string is built.
const estimatedAvgTagLen = 16

// WithTags returns a LogOption that appends the tags for the telemetry log message. Tags are key-value pairs that are then
// serialized into a simple "key:value,key2:value2" format. No quoting or escaping is performed.
// Multiple calls to WithTags will append tags without duplications, preserving the order of first occurrence.
func WithTags(tags []string) LogOption {
	return func(key *loggerKey, _ *loggerValue) {
		if key == nil || len(tags) == 0 {
			return
		}

		if key.tags == "" {
			key.tags = strings.Join(tags, ",")
			return
		}

		seen := make(map[string]struct{}, len(tags))

		var builder strings.Builder
		builder.Grow(len(key.tags) + len(tags)*estimatedAvgTagLen)

		for tag := range strings.SplitSeq(key.tags, ",") {
			if builder.Len() > 0 {
				builder.WriteByte(',')
			}
			builder.WriteString(tag)
			seen[tag] = struct{}{}
		}

		for _, tag := range tags {
			if _, exists := seen[tag]; !exists {
				seen[tag] = struct{}{}
				builder.WriteByte(',')
				builder.WriteString(tag)
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
