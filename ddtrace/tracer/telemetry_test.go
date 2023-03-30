// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

func TestTelemetryEnabled(t *testing.T) {
	t.Setenv("DD_TRACE_STARTUP_LOGS", "0")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wantEvents := []telemetry.RequestType{telemetry.RequestTypeAppStarted, telemetry.RequestTypeDependenciesLoaded}
	client, wait, cleanup := telemetry.TestHTTPClient(t, ctx, wantEvents, nil)
	defer cleanup()

	Start(
		WithHTTPClient(client),
		WithDebugStack(false),
		WithService("test-serv"),
		WithEnv("test-env"),
		WithRuntimeMetrics(),
	)
	defer Stop()

	bodies := wait()
	assert.Len(t, bodies, len(wantEvents))
	var startPayload *telemetry.AppStarted = bodies[0].Payload.(*telemetry.AppStarted)

	telemetry.Check(startPayload.Configuration, t, "trace_debug_enabled", false)
	telemetry.Check(startPayload.Configuration, t, "service", "test-serv")
	telemetry.Check(startPayload.Configuration, t, "env", "test-env")
	telemetry.Check(startPayload.Configuration, t, "runtime_metrics_enabled", true)
}
