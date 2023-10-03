// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry/telemetrytest"

	"github.com/stretchr/testify/assert"
)

// Test that the profiler sends the correct telemetry information
func TestTelemetryEnabled(t *testing.T) {
	t.Run("tracer start, profiler start", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		defer telemetry.MockGlobalClient(telemetryClient)()

		tracer.Start()
		defer tracer.Stop()

		Start(
			WithProfileTypes(
				HeapProfile,
			),
		)
		defer Stop()

		assert.True(t, telemetryClient.ProfilerEnabled)
		telemetry.Check(t, telemetryClient.Configuration, "heap_profile_enabled", true)
		telemetryClient.AssertNumberOfCalls(t, "ApplyOps", 2)
	})
	t.Run("only profiler start", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		defer telemetry.MockGlobalClient(telemetryClient)()
		Start(
			WithProfileTypes(
				HeapProfile,
			),
		)
		defer Stop()

		assert.True(t, telemetryClient.ProfilerEnabled)
		telemetry.Check(t, telemetryClient.Configuration, "heap_profile_enabled", true)
		telemetryClient.AssertNumberOfCalls(t, "ApplyOps", 1)
	})
}
