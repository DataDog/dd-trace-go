// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

// Test that telemetry is enabled by default for the profiler. Turning on the profiler should send two telemetry events:
// `app-product-change` to inform that the profiler has been enabled
// `app-client-configuration-change` to send profiler-related configuration information
func TestTelemetryEnabled(t *testing.T) {
	t.Setenv("DD_TELEMETRY_HEARTBEAT_INTERVAL", "1")
	receivedProducts := make(chan *telemetry.Products, 1)
	configChanges := make(chan *telemetry.ConfigurationChange, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/telemetry/proxy/api/v2/apmtelemetry" {
			return
		}
		if r.Header.Get("DD-Telemetry-Request-Type") == string(telemetry.RequestTypeAppProductChange) {
			var body telemetry.Body
			body.Payload = new(telemetry.Products)
			err := json.NewDecoder(r.Body).Decode(&body)
			if err != nil {
				t.Errorf("bad body: %s", err)
			}
			select {
			case receivedProducts <- body.Payload.(*telemetry.Products):
			default:
			}
		}
		if r.Header.Get("DD-Telemetry-Request-Type") == string(telemetry.RequestTypeAppClientConfigurationChange) {
			var body telemetry.Body
			body.Payload = new(telemetry.ConfigurationChange)
			err := json.NewDecoder(r.Body).Decode(&body)
			if err != nil {
				t.Errorf("bad body: %s", err)
			}
			select {
			case configChanges <- body.Payload.(*telemetry.ConfigurationChange):
			default:
			}
		}
	}))
	defer server.Close()
	Start(
		WithAgentAddr(server.Listener.Addr().String()),
		WithProfileTypes(
			BlockProfile,
			HeapProfile,
			MutexProfile,
		),
		WithPeriod(10*time.Millisecond),
		CPUDuration(1*time.Millisecond),
	)
	defer Stop()

	var productsPayload *telemetry.Products = <-receivedProducts
	assert.Equal(t, productsPayload.Profiler.Enabled, true)

	var configPayload *telemetry.ConfigurationChange = <-configChanges
	check := func(key string, expected interface{}) {
		for _, kv := range configPayload.Configuration {
			if kv.Name == key {
				if kv.Value != expected {
					t.Errorf("configuration %s: wanted %v, got %T", key, expected, kv.Value)
				}
				return
			}
		}
		t.Errorf("missing configuration %s", key)
	}

	check("heap_profile_enabled", true)
	check("block_profile_enabled", true)
	check("goroutine_profile_enabled", false)
	check("mutex_profile_enabled", true)
	check("profile_period", time.Duration(10*time.Millisecond).String())
	check("cpu_duration", time.Duration(1*time.Millisecond).String())
}
