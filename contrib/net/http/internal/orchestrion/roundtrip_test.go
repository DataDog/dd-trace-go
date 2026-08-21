// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package orchestrion

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func setOTelSemantics(t *testing.T, enabled bool) {
	t.Helper()
	oldValue, wasSet := os.LookupEnv("DD_TRACE_OTEL_SEMANTICS_ENABLED")
	require.NoError(t, os.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", strconv.FormatBool(enabled)))
	require.NoError(t, tracer.Start(tracer.WithTraceEnabled(false)))
	t.Cleanup(func() {
		if wasSet {
			require.NoError(t, os.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", oldValue))
		} else {
			require.NoError(t, os.Unsetenv("DD_TRACE_OTEL_SEMANTICS_ENABLED"))
		}
		require.NoError(t, tracer.Start(tracer.WithTraceEnabled(false)))
		tracer.Stop()
	})
}

func resetRoundTripperConfig(t *testing.T) {
	t.Helper()
	cfg = nil
	cfgOnce = sync.Once{}
	t.Cleanup(func() {
		cfg = nil
		cfgOnce = sync.Once{}
	})
}

func TestDefaultRoundTripperOTelSemanticConfig(t *testing.T) {
	setOTelSemantics(t, true)

	t.Run("defaults", func(t *testing.T) {
		resetRoundTripperConfig(t)
		t.Setenv("DD_TRACE_HTTP_CLIENT_RESOURCE_NAME_QUANTIZE", "true")
		got := defaultRoundTripperConfig()
		require.True(t, got.OTelSemanticsEnabled)
		assert.True(t, got.IsStatusError(500))
		assert.Equal(t, "GET", got.ResourceNamer(httptest.NewRequest(http.MethodGet, "http://example.com/users/123", nil)))
		emptyMethodRequest := httptest.NewRequest(http.MethodGet, "http://example.com/users/123", nil)
		emptyMethodRequest.Method = ""
		assert.Equal(t, "GET", got.ResourceNamer(emptyMethodRequest))
		assert.Equal(t, "HTTP", got.ResourceNamer(httptest.NewRequest("PROPFIND", "http://example.com/users/123", nil)))
	})

	t.Run("custom error statuses", func(t *testing.T) {
		resetRoundTripperConfig(t)
		t.Setenv("DD_TRACE_HTTP_CLIENT_ERROR_STATUSES", "500-510")
		got := defaultRoundTripperConfig()
		assert.False(t, got.IsStatusError(400))
		assert.True(t, got.IsStatusError(500))
	})

}

func TestDefaultRoundTripperLegacyConfig(t *testing.T) {
	setOTelSemantics(t, false)
	resetRoundTripperConfig(t)

	got := defaultRoundTripperConfig()
	assert.False(t, got.OTelSemanticsEnabled)
	assert.False(t, got.IsStatusError(500))
	assert.Equal(t, "GET /users/123", got.ResourceNamer(httptest.NewRequest(http.MethodGet, "http://example.com/users/123", nil)))
}
