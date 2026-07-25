// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/stretchr/testify/require"
)

func TestResolveProfilerSnapshotResamplesEachCall(t *testing.T) {
	t.Setenv("DD_SITE", "datadoghq.com")
	t.Setenv("DD_SERVICE", "first")
	t.Setenv("DD_PROFILING_ENABLED", "auto")

	first, _ := ResolveProfilerSnapshot()
	require.Equal(t, "datadoghq.com", first.Site)
	require.Equal(t, "first", first.Service)
	require.True(t, first.Enabled)
	require.Equal(t, "auto", first.Activation)

	t.Setenv("DD_SITE", "datadoghq.eu")
	t.Setenv("DD_SERVICE", "second")
	t.Setenv("DD_PROFILING_ENABLED", "false")

	second, _ := ResolveProfilerSnapshot()
	require.Equal(t, "datadoghq.eu", second.Site)
	require.Equal(t, "second", second.Service)
	require.False(t, second.Enabled)
	require.Equal(t, "manual", second.Activation)
}

func TestResolveProfilerSnapshotEnablementParsingIsExact(t *testing.T) {
	for _, test := range []struct {
		raw        string
		enabled    bool
		activation string
	}{
		{raw: "", enabled: true, activation: "manual"},
		{raw: "auto", enabled: true, activation: "auto"},
		{raw: "AUTO", enabled: true, activation: "manual"},
		{raw: " auto ", enabled: true, activation: "manual"},
		{raw: "invalid", enabled: true, activation: "manual"},
		{raw: "FALSE", enabled: false, activation: "manual"},
		{raw: "0", enabled: false, activation: "manual"},
		{raw: "1", enabled: true, activation: "manual"},
	} {
		t.Run(fmt.Sprintf("%q", test.raw), func(t *testing.T) {
			t.Setenv("DD_PROFILING_ENABLED", test.raw)

			snapshot, _ := ResolveProfilerSnapshot()

			require.Equal(t, test.enabled, snapshot.Enabled)
			require.Equal(t, test.activation, snapshot.Activation)
		})
	}
}

func TestResolveProfilerSnapshotPreservesParserBoundaries(t *testing.T) {
	t.Setenv("DD_PROFILING_UPLOAD_TIMEOUT", "not-a-duration")
	t.Setenv("DD_PROFILING_EXECUTION_TRACE_ENABLED", "true")
	t.Setenv("DD_PROFILING_EXECUTION_TRACE_PERIOD", "0")
	t.Setenv("DD_PROFILING_EXECUTION_TRACE_LIMIT_BYTES", strings.Repeat("9", 100))

	snapshot, _ := ResolveProfilerSnapshot()

	require.EqualError(t, snapshot.UploadTimeoutError, `DD_PROFILING_UPLOAD_TIMEOUT: time: invalid duration "not-a-duration"`)
	require.Equal(t, 10*time.Second, snapshot.UploadTimeout)
	require.True(t, snapshot.ExecutionTrace.Enabled)
	require.Zero(t, snapshot.ExecutionTrace.Period)
	require.Equal(t, 5*1024*1024, snapshot.ExecutionTrace.Limit)
}

func TestResolveProfilerSnapshotPreservesExplicitExecutionTraceZero(t *testing.T) {
	t.Setenv("DD_PROFILING_EXECUTION_TRACE_ENABLED", "true")
	t.Setenv("DD_PROFILING_EXECUTION_TRACE_PERIOD", "0s")
	t.Setenv("DD_PROFILING_EXECUTION_TRACE_LIMIT_BYTES", "0")

	snapshot, _ := ResolveProfilerSnapshot()

	require.True(t, snapshot.ExecutionTrace.Enabled)
	require.Zero(t, snapshot.ExecutionTrace.Period)
	require.Zero(t, snapshot.ExecutionTrace.Limit)
}

func TestResolveProfilerSnapshotPreservesNativeParserWarnings(t *testing.T) {
	logger := new(log.RecordLogger)
	defer log.UseLogger(logger)()
	t.Setenv("DD_PROFILING_EXECUTION_TRACE_PERIOD", "invalid")
	t.Setenv("DD_PROFILING_EXECUTION_TRACE_LIMIT_BYTES", strings.Repeat("9", 100))

	ResolveProfilerSnapshot()

	logs := strings.Join(logger.Logs(), "\n")
	require.Contains(t, logs,
		"Non-duration value for env var DD_PROFILING_EXECUTION_TRACE_PERIOD, defaulting to 900000000000.")
	require.Contains(t, logs,
		"Non-integer value for env var DD_PROFILING_EXECUTION_TRACE_LIMIT_BYTES, defaulting to 5242880.")
}

func TestResolveProfilerSnapshotCopiesTags(t *testing.T) {
	t.Setenv("DD_TAGS", "team:profiling,region:test")

	first, _ := ResolveProfilerSnapshot()
	require.Equal(t, "profiling", first.Tags["team"])
	first.Tags["team"] = "mutated"

	second, _ := ResolveProfilerSnapshot()
	require.Equal(t, "profiling", second.Tags["team"])
}

func TestResolveProfilerSnapshotEventPolicies(t *testing.T) {
	t.Setenv("DD_API_KEY", "secret-api-key")
	t.Setenv("DD_PROFILING_OUTPUT_DIR", "/private/output")
	t.Setenv("DD_PROFILING_URL", "https://user:password@example.test/profiles")
	t.Setenv("DD_TRACE_AGENT_URL", "https://agent:secret@example.test:8126")
	t.Setenv("DD_TAGS", "secret:tag")

	_, events := ResolveProfilerSnapshot()

	byName := make(map[string][]ConfigEvent)
	for _, event := range events {
		if event.Origin == telemetry.OriginEnvVar {
			byName[event.Name] = append(byName[event.Name], event)
		}
	}
	for _, name := range []string{"DD_API_KEY", "DD_PROFILING_OUTPUT_DIR", "DD_TAGS"} {
		require.NotEmpty(t, byName[name], name)
		for _, event := range byName[name] {
			require.Equal(t, TelemetryOmit, event.Policy, name)
			require.Nil(t, event.Value, name)
			require.NotContains(t, fmt.Sprint(event.Err), "secret", name)
		}
	}
	for _, name := range []string{"DD_PROFILING_URL", "DD_TRACE_AGENT_URL"} {
		require.NotEmpty(t, byName[name], name)
		for _, event := range byName[name] {
			require.Equal(t, TelemetrySanitizeURL, event.Policy, name)
			require.NotContains(t, fmt.Sprint(event.Value), "password", name)
			require.NotContains(t, fmt.Sprint(event.Value), "secret", name)
		}
	}
}

func TestProfilerBindingsKeepOnlyEnablementStable(t *testing.T) {
	raw, bindings := RegisteredDefinitions()
	rawSources := make(map[string]SourcePolicy, len(raw))
	for _, definition := range raw {
		rawSources[definition.Key] = definition.Sources
	}
	var start, enablement ConsumerBinding
	for _, binding := range bindings {
		switch binding.ID {
		case "profiler.start":
			start = binding
		case "profiler.enablement":
			enablement = binding
		}
	}
	require.NotEmpty(t, start.Keys)
	require.True(t, start.EnvironmentOnly)
	require.Equal(t, []string{"DD_PROFILING_ENABLED"}, enablement.Keys)
	require.False(t, enablement.EnvironmentOnly)
	require.Equal(t, SourceStable, rawSources["DD_PROFILING_ENABLED"])
}
