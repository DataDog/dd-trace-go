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

func TestTelemetryEnabled(t *testing.T) {
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
	telemetry.Check(t, telemetryClient.Configuration, "trace_debug_enabled", false)
	telemetry.Check(t, telemetryClient.Configuration, "service", "test-serv")
	telemetry.Check(t, telemetryClient.Configuration, "env", "test-env")
	telemetry.Check(t, telemetryClient.Configuration, "runtime_metrics_enabled", true)
}
