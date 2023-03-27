// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// Package telemetry implements a client for sending telemetry information to
// Datadog regarding usage of an APM library such as tracing or profiling.

package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestProductChange(t *testing.T) {
	t.Setenv("DD_TELEMETRY_HEARTBEAT_INTERVAL", "1")
	receivedProducts := make(chan *Products, 1)
	receivedConfigs := make(chan *ConfigurationChange, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("DD-Telemetry-Request-Type") == string(RequestTypeAppProductChange) {
			var body Body
			body.Payload = new(Products)
			err := json.NewDecoder(r.Body).Decode(&body)
			if err != nil {
				t.Errorf("bad body: %s", err)
			}
			select {
			case receivedProducts <- body.Payload.(*Products):
			default:
			}
		}
		if r.Header.Get("DD-Telemetry-Request-Type") == string(RequestTypeAppClientConfigurationChange) {
			var body Body
			body.Payload = new(ConfigurationChange)
			err := json.NewDecoder(r.Body).Decode(&body)
			if err != nil {
				t.Errorf("bad body: %s", err)
			}
			select {
			case receivedConfigs <- body.Payload.(*ConfigurationChange):
			default:
			}
		}
	}))
	defer server.Close()
	client := &Client{
		URL: server.URL,
	}
	client.start(nil, NamespaceProfilers)
	client.productChange(NamespaceProfilers, true,
		[]Configuration{BoolConfig("delta_profiles", true)})

	var productsPayload *Products = <-receivedProducts
	assert.Equal(t, productsPayload.Profiler.Enabled, true)

	var configPayload *ConfigurationChange = <-receivedConfigs
	check := func(key string, expected interface{}) {
		for _, kv := range configPayload.Configuration {
			if kv.Name == key {
				if kv.Value != expected {
					t.Errorf("configuration %s: wanted %v, got %v", key, expected, kv.Value)
				}
				return
			}
		}
		t.Errorf("missing configuration %s", key)
	}
	check("delta_profiles", true)
}

func mockServer(ctx context.Context, t *testing.T, expectedHits int, telemetry func()) (wait func() []string, cleanup func()) {
	messages := make([]string, expectedHits)
	hits := 0
	done := make(chan struct{})
	mu := sync.Mutex{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/telemetry/proxy/api/v2/apmtelemetry" {
			return
		}
		rType := RequestType(r.Header.Get("DD-Telemetry-Request-Type"))
		if rType != RequestTypeAppClientConfigurationChange && rType != RequestTypeAppProductChange && rType != RequestTypeAppStarted && rType != RequestTypeDependenciesLoaded {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if hits == expectedHits {
			t.Fatalf("too many telemetry messages (expected %d)", expectedHits)
		}
		messages[hits] = string(rType)
		hits += 1
		if hits == expectedHits {
			done <- struct{}{}
		}
	}))
	prevURL := GlobalClient.URL
	GlobalClient.ApplyOps(WithURL(false, server.URL))
	return func() []string {
			telemetry()
			select {
			case <-ctx.Done():
				t.Fatal("TestProductStart timed out")
			case <-done:
			}
			return messages
		}, func() {
			server.Close()
			GlobalClient.ApplyOps(WithURL(false, prevURL))
			GlobalClient.Stop()
		}
}

func TestProductStart(t *testing.T) {
	// this test is meant to ensure that a given sequence of ProductStart/ProductStop calls
	// emits the expected telemetry events.
	t.Setenv("DD_TELEMETRY_HEARTBEAT_INTERVAL", "1")
	t.Setenv("DD_TRACE_STARTUP_LOGS", "0")
	tests := []struct {
		name           string
		wantedMessages []string
		telemetry      func()
	}{
		{
			name:           "tracer start, profiler start with config",
			wantedMessages: []string{"app-started", "app-dependencies-loaded", "app-client-configuration-change", "app-product-change"},
			telemetry: func() {
				ProductStart(NamespaceTracers, []Configuration{})
				ProductStart(NamespaceProfilers, []Configuration{{Name: "key", Value: "value"}})
			},
		},
		{
			name:           "profiler start, tracer start, profiler stop",
			wantedMessages: []string{"app-started", "app-dependencies-loaded", "app-client-configuration-change", "app-product-change", "app-product-change"},
			telemetry: func() {
				ProductStart(NamespaceProfilers, nil)
				ProductStart(NamespaceTracers, []Configuration{{Name: "key", Value: "value"}})
				ProductStop(NamespaceProfilers)
			},
		},
		{
			name:           "profiler start, profiler stop, tracer start",
			wantedMessages: []string{"app-started", "app-dependencies-loaded", "app-product-change", "app-client-configuration-change", "app-product-change"},
			telemetry: func() {
				ProductStart(NamespaceProfilers, nil)
				ProductStop(NamespaceProfilers)
				ProductStart(NamespaceTracers, []Configuration{{Name: "key", Value: "value"}})
			},
		},
		{
			name:           "tracer start, tracer stop, profiler start, profiler stop",
			wantedMessages: []string{"app-started", "app-dependencies-loaded", "app-product-change", "app-product-change"},
			telemetry: func() {
				ProductStart(NamespaceTracers, nil)
				ProductStop(NamespaceProfilers)
				ProductStart(NamespaceProfilers, nil)
				ProductStop(NamespaceProfilers)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			wait, cleanup := mockServer(ctx, t, len(test.wantedMessages), test.telemetry)
			defer cleanup()
			messages := wait()
			for i := range messages {
				assert.Equal(t, test.wantedMessages[i], messages[i])
			}
		})
	}
}
