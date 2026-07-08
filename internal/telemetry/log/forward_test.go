// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

func TestForwardError_ConstantMessageAsKey(t *testing.T) {
	var captured telemetry.Record

	orig := sendLog
	defer func() { sendLog = orig }()
	sendLog = func(r telemetry.Record, _ ...telemetry.LogOption) { captured = r }

	forwardError("operation failed: %s", []any{errors.New("secret detail")})

	assert.Equal(t, "operation failed: %s", captured.Message,
		"format string must be used as-is (never interpolated)")
	assert.Equal(t, slog.LevelError, captured.Level)
}

func TestForwardError_ErrorArgScrubbed(t *testing.T) {
	var captured telemetry.Record

	orig := sendLog
	defer func() { sendLog = orig }()
	sendLog = func(r telemetry.Record, _ ...telemetry.LogOption) { captured = r }

	sentinel := errors.New("password=secret123")
	forwardError("something failed: %s", []any{sentinel})

	// Verify no sensitive data leaked through attributes.
	found := false
	captured.Attrs(func(a slog.Attr) bool {
		v := a.Value.String()
		assert.NotContains(t, v, "secret123", "error message must not appear in telemetry")
		if a.Key == "error" {
			found = true
		}
		return true
	})
	require.True(t, found, "error attribute must be present")
}

func TestForwardError_NonErrorArgIgnored(t *testing.T) {
	var captured telemetry.Record

	orig := sendLog
	defer func() { sendLog = orig }()
	sendLog = func(r telemetry.Record, _ ...telemetry.LogOption) { captured = r }

	forwardError("count is %d", []any{42})

	hasAttr := false
	captured.Attrs(func(a slog.Attr) bool { hasAttr = true; return false })
	assert.False(t, hasAttr, "non-error arguments must not be attached")
}

func TestForwardError_ExcludedTemplate(t *testing.T) {
	called := false

	orig := sendLog
	defer func() { sendLog = orig }()
	sendLog = func(_ telemetry.Record, _ ...telemetry.LogOption) { called = true }

	forwardError("failure sending traces (attempt %d of %d): %v", []any{1, 3, errors.New("timeout")})

	assert.False(t, called, "excluded templates must not reach telemetry")
}

func TestForwardError_DowngradedTemplate(t *testing.T) {
	var captured telemetry.Record

	orig := sendLog
	defer func() { sendLog = orig }()
	sendLog = func(r telemetry.Record, _ ...telemetry.LogOption) { captured = r }

	forwardError("config: usage of a unlisted environment variable: %s", []any{"MY_VAR"})

	assert.Equal(t, slog.LevelWarn, captured.Level,
		"downgraded templates must be sent as warnings")
}

func TestForwardError_StacktraceOption(t *testing.T) {
	var capturedOpts []telemetry.LogOption

	orig := sendLog
	defer func() { sendLog = orig }()
	sendLog = func(_ telemetry.Record, opts ...telemetry.LogOption) { capturedOpts = opts }

	forwardError("sdk defect occurred", nil)

	assert.Len(t, capturedOpts, 1, "exactly one option (WithStacktrace) must be passed")
}
