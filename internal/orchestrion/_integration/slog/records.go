// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package slog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// record holds the keys of one JSON log record. A key that appears twice keeps
// both values, so a record correlated twice is visible.
type record map[string][]json.RawMessage

// group returns the keys of the object under key, or nil.
func (r record) group(t *testing.T, key string) record {
	t.Helper()
	values := r[key]
	if len(values) != 1 {
		return nil
	}
	g, err := parseRecord(values[0])
	require.NoError(t, err)
	return g
}

// parseRecords parses the JSON lines in data and returns them by message.
// Lines that are not JSON objects are ignored.
func parseRecords(t *testing.T, data []byte) map[string]record {
	t.Helper()
	records := make(map[string]record)
	s := bufio.NewScanner(bytes.NewReader(data))
	for s.Scan() {
		line := bytes.TrimSpace(s.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		rec, err := parseRecord(line)
		require.NoErrorf(t, err, "parsing %s", line)
		require.Lenf(t, rec["msg"], 1, "record %s", line)
		var msg string
		require.NoError(t, json.Unmarshal(rec["msg"][0], &msg))
		require.NotContainsf(t, records, msg, "two records with message %q", msg)
		records[msg] = rec
	}
	require.NoError(t, s.Err())
	return records
}

func parseRecord(data []byte) (record, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	rec := make(record)
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		key := tok.(string)
		rec[key] = append(rec[key], value)
	}
	return rec, nil
}

// requireCorrelated checks that rec holds the IDs of span exactly once each.
func requireCorrelated(t *testing.T, rec record, span *tracer.Span) {
	t.Helper()
	requireIDs(t, rec, span.Context().TraceID(), strconv.FormatUint(span.Context().SpanID(), 10))
}

// requireIDs checks that rec holds traceID and spanID exactly once each.
func requireIDs(t *testing.T, rec record, traceID, spanID string) {
	t.Helper()
	require.NotNil(t, rec, "missing record")
	require.Equal(t, []string{traceID}, stringValues(t, rec[ext.LogKeyTraceID]), ext.LogKeyTraceID)
	require.Equal(t, []string{spanID}, stringValues(t, rec[ext.LogKeySpanID]), ext.LogKeySpanID)
}

// requireNotCorrelated checks that rec holds no trace or span ID.
func requireNotCorrelated(t *testing.T, rec record) {
	t.Helper()
	require.NotNil(t, rec, "missing record")
	require.NotContains(t, rec, ext.LogKeyTraceID)
	require.NotContains(t, rec, ext.LogKeySpanID)
}

func stringValues(t *testing.T, values []json.RawMessage) []string {
	t.Helper()
	out := make([]string, 0, len(values))
	for _, v := range values {
		var s string
		require.NoError(t, json.Unmarshal(v, &s))
		out = append(out, s)
	}
	return out
}
