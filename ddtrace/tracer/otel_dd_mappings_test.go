// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry/telemetrytest"
)

func TestAssessSource(t *testing.T) {
	t.Run("invalid", func(t *testing.T) {
		assert.Panics(t, func() { getDDorOtelConfig("invalid") }, "invalid config should panic")
	})

	t.Run("dd", func(t *testing.T) {
		t.Setenv("DD_SERVICE", "abc")
		v := getDDorOtelConfig("service")
		assert.Equal(t, "abc", v)
	})
	t.Run("ot", func(t *testing.T) {
		t.Setenv("OTEL_SERVICE_NAME", "abc")
		v := getDDorOtelConfig("service")
		assert.Equal(t, "abc", v)
	})
	t.Run("both", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		defer telemetry.MockGlobalClient(telemetryClient)()
		// DD_SERVICE prevails
		t.Setenv("DD_SERVICE", "abc")
		t.Setenv("OTEL_SERVICE_NAME", "123")
		v := getDDorOtelConfig("service")
		assert.Equal(t, "abc", v)
		telemetryClient.AssertCalled(t, "Count", telemetry.NamespaceTracers, "otel.env.hiding", 1.0, []string{"config.datadog:DD_SERVICE", "config.opentelemetry:OTEL_SERVICE_NAME"}, true)
	})
	t.Run("invalid-ot", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		defer telemetry.MockGlobalClient(telemetryClient)()
		t.Setenv("OTEL_LOG_LEVEL", "nonesense")
		v := getDDorOtelConfig("debugMode")
		assert.Equal(t, "", v)
		telemetryClient.AssertCalled(t, "Count", telemetry.NamespaceTracers, "otel.env.invalid", 1.0, []string{"config.datadog:DD_TRACE_DEBUG", "config.opentelemetry:OTEL_LOG_LEVEL"}, true)
	})
}
