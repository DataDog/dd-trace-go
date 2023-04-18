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
	"gopkg.in/DataDog/dd-trace-go.v1/profiler"
)

func TestTelemetryEnabled(t *testing.T) {
	t.Run("tracer start", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		defer telemetry.MockGlobalClient(telemetryClient)()

		Start(
			WithDebugStack(false),
			WithService("test-serv"),
			WithEnv("test-env"),
			WithRuntimeMetrics(),
		)
		defer Stop()

		assert.True(t, telemetryClient.Started)
		assert.True(t, telemetryClient.AsmEnabled)
		telemetry.Check(t, telemetryClient.Configuration, "trace_debug_enabled", false)
		telemetry.Check(t, telemetryClient.Configuration, "service", "test-serv")
		telemetry.Check(t, telemetryClient.Configuration, "env", "test-env")
		telemetry.Check(t, telemetryClient.Configuration, "runtime_metrics_enabled", true)
		if metrics, ok := telemetryClient.Metrics[telemetry.NamespaceTracers]; ok {
			if initTime, ok := metrics["tracer_init_time"]; ok {
				assert.True(t, initTime > 0)
				return
			}
			t.Fatalf("could not find tracer init time in telemetry client metrics")
		}
		t.Fatalf("could not find tracer namespace in telemetry client metrics")
	})
	t.Run("profiler start, tracer start", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		defer telemetry.MockGlobalClient(telemetryClient)()
		profiler.Start()
		defer profiler.Stop()
		Start(
			WithService("test-serv"),
		)
		defer Stop()
		telemetry.Check(t, telemetryClient.Configuration, "service", "test-serv")
	})
}
