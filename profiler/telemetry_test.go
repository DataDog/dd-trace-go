// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"

	"github.com/stretchr/testify/assert"
)

// Test that the profiler sends the correct telemetry information
func TestTelemetryEnabled(t *testing.T) {
	t.Run("tracer start, profiler start", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		tracer.Start()
		defer tracer.Stop()

		Start(
			WithProfileTypes(
				HeapProfile,
			),
		)
		defer Stop()

		assert.True(t, telemetryClient.Products[telemetry.NamespaceProfilers])
		assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{Name: "heap_profile_enabled", Value: true})
	})
	t.Run("only profiler start", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()
		Start(
			WithProfileTypes(
				HeapProfile,
			),
		)
		defer Stop()

		assert.True(t, telemetryClient.Products[telemetry.NamespaceProfilers])
		assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{Name: "heap_profile_enabled", Value: true})
	})
}
