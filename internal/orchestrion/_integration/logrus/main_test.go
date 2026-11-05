// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package logrus

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestLoggersInMain covers loggers built in package main, including one
// imported under another name. The harness suites never build a package main.
func TestLoggersInMain(t *testing.T) {
	bin := maintest.Build(t, "./logrus/mainapp", true)
	agent := maintest.NewAgent(t)
	out := agent.Exec(t, bin)
	t.Logf("app output:\n%s", out)

	lister, ok := agent.Agent.(agenttest.SpanLister)
	require.True(t, ok, "the test agent must list its spans")
	spans := lister.Spans()
	require.Len(t, spans, 1)
	root := spans[0]
	require.Equal(t, "mainapp.root", root.Operation)

	loggers := []string{"new", "standard", "literal_ptr", "literal_val", "literal_no_hooks", "alias", "contrib"}
	hooks := map[string]string{}
	entries := map[string][]map[string]any{}
	s := bufio.NewScanner(bytes.NewReader(out))
	for s.Scan() {
		line := s.Text()
		if rest, ok := strings.CutPrefix(line, "hooks "); ok {
			name, counts, _ := strings.Cut(rest, " ")
			hooks[name] = counts
			continue
		}
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		name, _ := entry["logger"].(string)
		entries[name] = append(entries[name], entry)
	}
	require.NoError(t, s.Err())

	for _, name := range loggers {
		assert.Equal(t, "1,1,1,1,1,1,1", hooks[name], "DDContextLogHook count per level for %s", name)
		require.Len(t, entries[name], 1, "entries of %s", name)
		entry := entries[name][0]

		traceID, _ := entry["dd.trace_id"].(string)
		require.Len(t, traceID, 32, "128-bit trace ID of %s", name)
		lower, err := strconv.ParseUint(traceID[16:], 16, 64)
		require.NoError(t, err)
		assert.Equal(t, root.TraceID, lower, "trace ID of %s", name)

		assert.Equal(t, map[string]any{
			"level":       "info",
			"msg":         "correlated",
			"logger":      name,
			"dd.trace_id": traceID,
			"dd.span_id":  strconv.FormatUint(root.SpanID, 10),
		}, entry, "fields of %s", name)
	}
	assert.Len(t, entries, len(loggers))
}
