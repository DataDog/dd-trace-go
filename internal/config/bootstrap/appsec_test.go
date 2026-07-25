// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package bootstrap

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

func TestAppSecStackTraceCachesFirstEnvironmentRead(t *testing.T) {
	ResetAppSecStackTraceForTesting()
	t.Cleanup(ResetAppSecStackTraceForTesting)
	t.Setenv("DD_APPSEC_STACK_TRACE_ENABLED", "true")
	t.Setenv("DD_APPSEC_MAX_STACK_TRACE_DEPTH", "64")

	first := AppSecStackTrace()
	t.Setenv("DD_APPSEC_STACK_TRACE_ENABLED", "false")
	t.Setenv("DD_APPSEC_MAX_STACK_TRACE_DEPTH", "1")
	second := AppSecStackTrace()

	require.Equal(t, AppSecStackTraceSnapshot{
		Enabled:        true,
		MaxDepth:       64,
		TopFrameDepth:  16,
		EnabledRaw:     "true",
		EnabledPresent: true,
		DepthRaw:       "64",
		DepthPresent:   true,
	}, first)
	require.Equal(t, first, second)
}

func TestAppSecStackTraceDisabledPreservesDefaultsAndRawDepth(t *testing.T) {
	ResetAppSecStackTraceForTesting()
	t.Cleanup(ResetAppSecStackTraceForTesting)
	t.Setenv("DD_APPSEC_STACK_TRACE_ENABLED", "false")
	t.Setenv("DD_APPSEC_MAX_STACK_TRACE_DEPTH", "17")

	got := AppSecStackTrace()

	require.Equal(t, AppSecStackTraceSnapshot{
		Enabled:        false,
		MaxDepth:       32,
		TopFrameDepth:  8,
		EnabledRaw:     "false",
		EnabledPresent: true,
		DepthRaw:       "17",
		DepthPresent:   true,
	}, got)
}

func TestClaimAppSecStackTraceTelemetryResetsWithSnapshot(t *testing.T) {
	ResetAppSecStackTraceForTesting()
	t.Cleanup(ResetAppSecStackTraceForTesting)
	t.Setenv("DD_APPSEC_STACK_TRACE_ENABLED", "true")

	first, firstReport := ClaimAppSecStackTraceTelemetry()
	second, secondReport := ClaimAppSecStackTraceTelemetry()
	ResetAppSecStackTraceForTesting()
	third, thirdReport := ClaimAppSecStackTraceTelemetry()

	require.Equal(t, first, second)
	require.Equal(t, first, third)
	require.True(t, firstReport)
	require.False(t, secondReport)
	require.True(t, thirdReport)
}

func TestAppSecStackTracePositiveOverflowPreservesRangeDiagnostic(t *testing.T) {
	ResetAppSecStackTraceForTesting()
	t.Cleanup(ResetAppSecStackTraceForTesting)

	overflow := "2147483648"
	if strconv.IntSize == 64 {
		overflow = "9223372036854775808"
	}
	t.Setenv("DD_APPSEC_MAX_STACK_TRACE_DEPTH", overflow)

	logger := new(log.RecordLogger)
	restoreLogger := log.UseLogger(logger)
	t.Cleanup(restoreLogger)

	got := AppSecStackTrace()
	log.Flush()
	logs := strings.Join(logger.Logs(), "\n")

	require.ErrorIs(t, got.DepthError, strconv.ErrRange)
	require.Contains(t, logs, "value out of range")
	require.NotContains(t, logs, "value is not a strictly positive integer")
}
