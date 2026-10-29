// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package zerolog

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
	bin := maintest.Build(t, "./zerolog/mainapp", true)
	agent := maintest.NewAgent(t)
	out := agent.Exec(t, bin)
	t.Logf("app output:\n%s", out)

	lister, ok := agent.Agent.(agenttest.SpanLister)
	require.True(t, ok, "the test agent must list its spans")
	spans := lister.Spans()
	require.Len(t, spans, 1)
	root := spans[0]
	require.Equal(t, "mainapp.root", root.Operation)

	// The contrib logger holds the rule's hook and the one main adds.
	wantHooks := map[string]string{
		"new": "1", "package_var": "1", "alias": "1", "output": "1",
		"global": "1", "contrib": "2", "dependency": "1",
	}
	hooks := map[string]string{}
	entries := map[string][]string{}
	s := bufio.NewScanner(bytes.NewReader(out))
	for s.Scan() {
		line := s.Text()
		if rest, ok := strings.CutPrefix(line, "hooks "); ok {
			name, n, _ := strings.Cut(rest, " ")
			hooks[name] = n
			continue
		}
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		name, _ := entry["logger"].(string)
		entries[name] = append(entries[name], line)
	}
	require.NoError(t, s.Err())
	assert.Equal(t, wantHooks, hooks, "DDContextLogHook count per logger")
	require.Len(t, entries, len(wantHooks))

	for name := range wantHooks {
		require.Len(t, entries[name], 1, "entries of %s", name)
		line := entries[name][0]
		// Unmarshal keeps the last of duplicate keys, so count them in the line.
		assert.Equal(t, 1, strings.Count(line, `"dd.trace_id"`), "dd.trace_id in %s", name)
		assert.Equal(t, 1, strings.Count(line, `"dd.span_id"`), "dd.span_id in %s", name)

		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		traceID, _ := entry["dd.trace_id"].(string)
		require.Len(t, traceID, 32, "128-bit trace ID of %s", name)
		lower, err := strconv.ParseUint(traceID[16:], 16, 64)
		require.NoError(t, err)
		assert.Equal(t, root.TraceID, lower, "trace ID of %s", name)

		delete(entry, "time") // the global logger adds it
		assert.Equal(t, map[string]any{
			"level":       "info",
			"message":     "correlated",
			"logger":      name,
			"dd.trace_id": traceID,
			"dd.span_id":  strconv.FormatUint(root.SpanID, 10),
		}, entry, "fields of %s", name)
	}
}
