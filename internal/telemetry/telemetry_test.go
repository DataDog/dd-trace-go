// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// Package telemetry implements a client for sending telemetry information to
// Datadog regarding usage of an APM library such as tracing or profiling.

package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProductEnabled(t *testing.T) {
	client := new(client)
	client.start(nil, NamespaceTracers, true)
	client.productChange(NamespaceProfilers, true)
	// should just contain app-product-change
	require.Len(t, client.requests, 1)
	body := client.requests[0].Body

	assert.Equal(t, RequestTypeAppProductChange, body.RequestType)
	var productsPayload = body.Payload.(*ProductsPayload)
	assert.True(t, productsPayload.Products.Profiler.Enabled)
}

func TestConfigChange(t *testing.T) {
	client := new(client)
	client.start(nil, NamespaceTracers, true)
	client.configChange([]Configuration{BoolConfig("delta_profiles", true)})
	require.Len(t, client.requests, 1)

	body := client.requests[0].Body
	assert.Equal(t, RequestTypeAppClientConfigurationChange, body.RequestType)
	var configPayload = client.requests[0].Body.Payload.(*ConfigurationChange)
	require.Len(t, configPayload.Configuration, 1)

	Check(t, configPayload.Configuration, "delta_profiles", true)
}

// mockServer initializes a server that expects a strict amount of telemetry events. It saves these
// events in a slice until the expected number of events is reached.
// the `genTelemetry` argument accepts a function that should generate the expected telemetry events via calls to the global client
// the `expectedHits` argument specifies the number of telemetry events the server should expect.
func mockServer(ctx context.Context, t *testing.T, expectedHits int, genTelemetry func(), exclude ...RequestType) (waitForEvents func() []RequestType, cleanup func()) {
	messages := make([]RequestType, expectedHits)
	hits := 0
	done := make(chan struct{})
	mu := sync.Mutex{}
	excludeEvent := make(map[RequestType]struct{})
	for _, event := range exclude {
		excludeEvent[event] = struct{}{}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/telemetry/proxy/api/v2/apmtelemetry" {
			return
		}
		rType := RequestType(r.Header.Get("DD-Telemetry-Request-Type"))
		if _, ok := excludeEvent[rType]; ok {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if hits == expectedHits {
			t.Fatalf("too many telemetry messages (expected %d)", expectedHits)
		}
		messages[hits] = rType
		if hits++; hits == expectedHits {
			done <- struct{}{}
		}
	}))
	GlobalClient.ApplyOps(WithURL(false, server.URL))

	return func() []RequestType {
			genTelemetry()
			select {
			case <-ctx.Done():
				t.Fatal("TestProductChange timed out")
			case <-done:
			}
			return messages
		}, func() {
			server.Close()
			GlobalClient.Stop()
		}
}

func TestProductChange(t *testing.T) {
	// this test is meant to ensure that a given sequence of ProductStart/ProductStop calls
	// emits the expected telemetry events.
	t.Setenv("DD_TELEMETRY_HEARTBEAT_INTERVAL", "1")
	t.Setenv("DD_TRACE_STARTUP_LOGS", "0")
	tests := []struct {
		name           string
		wantedMessages []RequestType
		genTelemetry   func()
	}{
		{
			name:           "tracer start, profiler start",
			wantedMessages: []RequestType{RequestTypeAppStarted, RequestTypeDependenciesLoaded, RequestTypeAppClientConfigurationChange, RequestTypeAppProductChange},
			genTelemetry: func() {
				GlobalClient.ProductChange(NamespaceTracers, true, nil)
				GlobalClient.ProductChange(NamespaceProfilers, true, []Configuration{{Name: "key", Value: "value"}})
			},
		},
		{
			name:           "profiler start, tracer start",
			wantedMessages: []RequestType{RequestTypeAppStarted, RequestTypeDependenciesLoaded, RequestTypeAppClientConfigurationChange},
			genTelemetry: func() {
				GlobalClient.ProductChange(NamespaceProfilers, true, nil)
				GlobalClient.ProductChange(NamespaceTracers, true, []Configuration{{Name: "key", Value: "value"}})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			telemetryClient := new(client)
			defer MockGlobalClient(telemetryClient)()
			excludedEvents := []RequestType{RequestTypeAppHeartbeat, RequestTypeGenerateMetrics, RequestTypeAppClosing}
			waitForEvents, cleanup := mockServer(ctx, t, len(test.wantedMessages), test.genTelemetry, excludedEvents...)
			defer cleanup()
			messages := waitForEvents()
			for i := range messages {
				assert.Equal(t, test.wantedMessages[i], messages[i])
			}
		})
	}
}

// Test that globally registered app config is sent in telemetry requests including the configuration state.
func TestRegisterAppConfig(t *testing.T) {
	client := new(client)
	client.RegisterAppConfig("key1", "val1", OriginDefault)

	// Test that globally registered app config is sent in app-started payloads
	client.start([]Configuration{{Name: "key2", Value: "val2", Origin: OriginDDConfig}}, NamespaceTracers, false)

	req := client.requests[0].Body
	require.Equal(t, RequestTypeAppStarted, req.RequestType)
	appStarted := req.Payload.(*AppStarted)
	cfg := appStarted.Configuration
	require.Contains(t, cfg, Configuration{Name: "key1", Value: "val1", Origin: OriginDefault})
	require.Contains(t, cfg, Configuration{Name: "key2", Value: "val2", Origin: OriginDDConfig})

	// Test that globally registered app config is sent in app-client-configuration-change payloads
	client.ProductChange(NamespaceTracers, true, []Configuration{{Name: "key3", Value: "val3", Origin: OriginCode}})

	req = client.requests[2].Body
	require.Equal(t, RequestTypeAppClientConfigurationChange, req.RequestType)
	appConfigChange := req.Payload.(*ConfigurationChange)
	cfg = appConfigChange.Configuration
	require.Len(t, cfg, 2)
	require.Contains(t, cfg, Configuration{Name: "key1", Value: "val1", Origin: OriginDefault})
	require.Contains(t, cfg, Configuration{Name: "key3", Value: "val3", Origin: OriginCode})
}
