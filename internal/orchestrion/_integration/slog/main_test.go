// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package slog

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestLoggersInMain covers slog loggers built in package main, including with
// log/slog imported under another name. The harness suites never build a
// package main. Each record must hold the IDs of the span once.
func TestLoggersInMain(t *testing.T) {
	bin := maintest.Build(t, "./slog/mainapp", true)
	out := maintest.NewAgent(t).Exec(t, bin)

	records := parseRecords(t, out)
	require.Len(t, records, 4, "output:\n%s", out)
	for _, msg := range []string{"main-new", "main-contrib", "main-alias"} {
		rec := records[msg]
		require.NotNilf(t, rec, "missing record %s", msg)
		requireIDs(t, rec, wantID(t, rec, "want_trace_id"), wantID(t, rec, "want_span_id"))
	}
	requireNotCorrelated(t, records["main-no-span"])
}

func wantID(t *testing.T, rec record, key string) string {
	t.Helper()
	require.Len(t, rec[key], 1, key)
	var id string
	require.NoError(t, json.Unmarshal(rec[key][0], &id))
	return id
}
