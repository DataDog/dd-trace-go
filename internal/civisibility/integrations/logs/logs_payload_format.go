// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package logs

type (
	// logsEntriesPayload represents a list of log entries to be sent.
	logsEntriesPayload []*logEntry

	// logEntryPayload represents a single log entry with its metadata.
	logEntry struct {
		DdSource   string `json:"ddsource"`
		Hostname   string `json:"hostname"`
		Timestamp  int64  `json:"timestamp,omitempty"`
		Message    string `json:"message"`
		DdTraceID  string `json:"dd.trace_id"`
		DdSpanID   string `json:"dd.span_id"`
		TestModule string `json:"test.module"`
		TestSuite  string `json:"test.suite"`
		TestName   string `json:"test.name"`
		Service    string `json:"service"`
		DdTags     string `json:"dd_tags,omitempty"`
	}
)
