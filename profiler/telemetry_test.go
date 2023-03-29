// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/httpmem"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

func telemetryHTTPClient(t *testing.T, ctx context.Context, events []telemetry.RequestType, ignore map[telemetry.RequestType]struct{}) (client *http.Client, wait func() []*telemetry.Body, cleanup func()) {
	eventsBuffer := make(chan *telemetry.Body, len(events))
	done := make(chan struct{})
	curEvent := 0
	mu := new(sync.Mutex)
	server, client := httpmem.ServerAndClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/telemetry/proxy/api/v2/apmtelemetry" {
			return
		}
		rType := telemetry.RequestType(r.Header.Get("DD-Telemetry-Request-Type"))
		mu.Lock()
		if _, ok := ignore[rType]; !ok && curEvent < len(events) && rType == events[curEvent] {
			var body telemetry.Body

			switch events[curEvent] {
			case telemetry.RequestTypeAppStarted:
				body.Payload = new(telemetry.AppStarted)
			case telemetry.RequestTypeDependenciesLoaded:
				body.Payload = new(telemetry.Dependencies)
			case telemetry.RequestTypeAppProductChange:
				body.Payload = new(telemetry.Products)
			case telemetry.RequestTypeAppClientConfigurationChange:
				body.Payload = new(telemetry.ConfigurationChange)
			}
			err := json.NewDecoder(r.Body).Decode(&body)
			if err != nil {
				t.Errorf("bad body: %s", err)
			}
			select {
			case eventsBuffer <- &body:
			default:
			}
			curEvent += 1
			if curEvent == len(events) {
				done <- struct{}{}
			}
		}
		mu.Unlock()
	}))

	return client, func() []*telemetry.Body {
			bodies := []*telemetry.Body{}
			select {
			case <-ctx.Done():
				t.Fatalf("Time out: waiting for telemetry payload")
			case <-done:
			}
			for _ = range events {
				bodies = append(bodies, <-eventsBuffer)
			}
			return bodies
		}, func() {
			server.Close()
		}
}

func check(t *testing.T, config []telemetry.Configuration, key string, expected interface{}) {
	for _, kv := range config {
		if kv.Name == key {
			if kv.Value != expected {
				t.Errorf("configuration %s: wanted %v, got %T", key, expected, kv.Value)
			}
			return
		}
	}
	t.Errorf("missing configuration %s", key)
}

// Test that the profiler can independently start telemetry
func TestTelemetryStart(t *testing.T) {
	t.Setenv("DD_TELEMETRY_HEARTBEAT_INTERVAL", "1")

	t.Run("profiler start", func(t *testing.T) {
		telemetry.Reset()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		wantEvents := []telemetry.RequestType{telemetry.RequestTypeAppStarted}
		client, wait, cleanup := telemetryHTTPClient(t, ctx, wantEvents, nil)
		defer cleanup()

		// start the profiler, then stop immediately
		Start(
			WithHTTPClient(client),
			WithProfileTypes(
				HeapProfile,
			),
		)
		defer Stop()

		bodies := wait()
		assert.Len(t, bodies, len(wantEvents))
		payload := bodies[0].Payload.(*telemetry.AppStarted)

		assert.True(t, payload.Products.Profiler.Enabled)
		check(t, payload.Configuration, "heap_profile_enabled", true)
	})

	t.Run("tracer start then profiler start", func(t *testing.T) {
		telemetry.Reset()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		wantEvents := []telemetry.RequestType{telemetry.RequestTypeAppClientConfigurationChange, telemetry.RequestTypeAppProductChange}
		client, wait, cleanup := telemetryHTTPClient(t, ctx, wantEvents, map[telemetry.RequestType]struct{}{telemetry.RequestTypeAppStarted: {}})
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
		check(t, configPayload.Configuration, "heap_profile_enabled", true)

		var productsPayload *telemetry.Products = bodies[1].Payload.(*telemetry.Products)
		assert.Equal(t, productsPayload.Profiler.Enabled, true)
	})

	t.Run("profiler stop", func(t *testing.T) {
		telemetry.Reset()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		wantEvents := []telemetry.RequestType{telemetry.RequestTypeAppProductChange}
		client, wait, cleanup := telemetryHTTPClient(t, ctx, wantEvents, map[telemetry.RequestType]struct{}{telemetry.RequestTypeAppStarted: {}})
		defer cleanup()

		// start the profiler, then stop immediately
		Start(
			WithHTTPClient(client),
			WithProfileTypes(
				HeapProfile,
			),
		)
		Stop()

		bodies := wait()
		assert.Len(t, bodies, len(wantEvents))
		payload := bodies[0].Payload.(*telemetry.Products)
		assert.Equal(t, payload.Profiler.Enabled, false)
	})
}
