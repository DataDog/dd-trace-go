// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

//go:generate go run github.com/tinylib/msgp -unexported -marshal=false -o=logs_payload_format_msgp.go -tests=false

package logs

import "github.com/tinylib/msgp/msgp"

type (
	// logsEntriesPayload represents a list of log entries to be sent.
	logsEntriesPayload []*logEntry

	// logEntryPayload represents a single log entry with its metadata.
	logEntry struct {
		DdSource string `json:"dd_source" msg:"dd_source"`
		DdTags   string `json:"dd_tags,omitempty" msg:"dd_tags,omitempty"`
		Hostname string `json:"hostname" msg:"hostname"`
		Message  string `json:"message" msg:"message"`
		Service  string `json:"service" msg:"service"`
	}
)

var (
	_ msgp.Encodable = (*logsEntriesPayload)(nil)
	_ msgp.Decodable = (*logsEntriesPayload)(nil)

	_ msgp.Encodable = (*logEntry)(nil)
	_ msgp.Decodable = (*logEntry)(nil)
)
