// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
	"github.com/DataDog/dd-trace-go/v2/profiler"

	"github.com/stretchr/testify/assert"
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
			WithPeerServiceMapping("key", "val"),
			WithPeerServiceDefaults(true),
			WithHeaderTags([]string{"key:val", "key2:val2"}),
		)
		defer globalconfig.SetServiceName("")
		defer Stop()

		assert.True(t, telemetryClient.Started)
		assert.True(t, telemetryClient.AsmEnabled)
		telemetryClient.AssertNumberOfCalls(t, "ApplyOps", 1)
		telemetry.Check(t, telemetryClient.Configuration, "trace_debug_enabled", false)
		telemetry.Check(t, telemetryClient.Configuration, "service", "test-serv")
		telemetry.Check(t, telemetryClient.Configuration, "env", "test-env")
		telemetry.Check(t, telemetryClient.Configuration, "runtime_metrics_enabled", true)
		telemetry.Check(t, telemetryClient.Configuration, "stats_computation_enabled", false)
		telemetry.Check(t, telemetryClient.Configuration, "trace_span_attribute_schema", 0)
		telemetry.Check(t, telemetryClient.Configuration, "trace_peer_service_defaults_enabled", true)
		telemetry.Check(t, telemetryClient.Configuration, "trace_peer_service_mapping", "key:val")
		telemetry.Check(t, telemetryClient.Configuration, "orchestrion_enabled", false)
		telemetry.Check(t, telemetryClient.Configuration, "trace_sample_rate", nil) // default value is NaN which is sanitized to nil
		telemetry.Check(t, telemetryClient.Configuration, "trace_header_tags", "key:val,key2:val2")
		if metrics, ok := telemetryClient.Metrics[telemetry.NamespaceGeneral]; ok {
			if initTime, ok := metrics["init_time"]; ok {
				assert.True(t, initTime > 0)
				return
			}
			t.Fatalf("could not find general init time in telemetry client metrics")
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
		defer globalconfig.SetServiceName("")
		defer Stop()
		telemetry.Check(t, telemetryClient.Configuration, "service", "test-serv")
		telemetryClient.AssertNumberOfCalls(t, "ApplyOps", 2)
	})
	t.Run("orchestrion telemetry", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		defer telemetry.MockGlobalClient(telemetryClient)()

		Start(WithOrchestrion(map[string]string{"k1": "v1", "k2": "v2"}))
		defer Stop()

		telemetry.Check(t, telemetryClient.Configuration, "orchestrion_enabled", true)
		telemetry.Check(t, telemetryClient.Configuration, "orchestrion_k1", "v1")
		telemetry.Check(t, telemetryClient.Configuration, "orchestrion_k2", "v2")
	})
}
