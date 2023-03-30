// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

// Test that the profiler can independently start telemetry
func TestTelemetryEnabled(t *testing.T) {
	t.Setenv("DD_TELEMETRY_HEARTBEAT_INTERVAL", "1")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wantEvents := []telemetry.RequestType{telemetry.RequestTypeAppClientConfigurationChange, telemetry.RequestTypeAppProductChange}
	ignoreEvents := []telemetry.RequestType{telemetry.RequestTypeAppStarted, telemetry.RequestTypeDependenciesLoaded}
	client, wait, cleanup := telemetry.TestHTTPClient(t, ctx, wantEvents, ignoreEvents)
	defer cleanup()

	tracer.Start(tracer.WithHTTPClient(client))
	defer tracer.Stop()
	Start(
		WithHTTPClient(client),
		WithProfileTypes(
			HeapProfile,
		),
	)
	defer Stop()

	bodies := wait()
	assert.Len(t, bodies, len(wantEvents))
	var configPayload *telemetry.ConfigurationChange = bodies[0].Payload.(*telemetry.ConfigurationChange)
	telemetry.Check(configPayload.Configuration, t, "heap_profile_enabled", true)

	var productsPayload *telemetry.Products = bodies[1].Payload.(*telemetry.Products)
	assert.Equal(t, productsPayload.Profiler.Enabled, true)

}
