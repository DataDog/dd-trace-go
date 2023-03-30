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
	"sync"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/httpmem"
)

// TestHTTPClient provides a test http client that waits for a specific sequence of telemetry events.
// It returns a function that waits for all expected events to be received, as well as a cleanup function.
func TestHTTPClient(t *testing.T, ctx context.Context, events []RequestType, ignoreEvents []RequestType) (client *http.Client, wait func() []*Body, cleanup func()) {
	eventsBuffer := make(chan *Body, len(events))
	done := make(chan struct{})
	curEvent := 0
	mu := new(sync.Mutex)

	ignore := make(map[RequestType]struct{})
	for _, event := range ignoreEvents {
		ignore[event] = struct{}{}
	}

	server, client := httpmem.ServerAndClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/telemetry/proxy/api/v2/apmtelemetry" {
			return
		}
		rType := RequestType(r.Header.Get("DD-Telemetry-Request-Type"))
		if _, ok := ignore[rType]; ok {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if curEvent >= len(events) {
			return
		} else if rType == events[curEvent] {
			var body Body
			switch events[curEvent] {
			case RequestTypeAppStarted:
				body.Payload = new(AppStarted)
			case RequestTypeDependenciesLoaded:
				body.Payload = new(Dependencies)
			case RequestTypeAppProductChange:
				body.Payload = new(Products)
			case RequestTypeAppClientConfigurationChange:
				body.Payload = new(ConfigurationChange)
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
				return
			}
		} else {
			t.Fatalf("Unexpected order of telemetry events")
		}
	}))

	return client, func() []*Body {
			bodies := []*Body{}
			select {
			case <-ctx.Done():
				t.Fatalf("Time out: waiting for telemetry payload")
			case <-done:
			}
			for range events {
				bodies = append(bodies, <-eventsBuffer)
			}
			return bodies
		}, func() {
			server.Close()
		}
}

// Check is a testing utility to assert that a target key value pair
// exists in an array of Configuration
func Check(configuration []Configuration, t *testing.T, key string, expected interface{}) {
	for _, kv := range configuration {
		if kv.Name == key {
			if kv.Value != expected {
				t.Errorf("configuration %s: wanted %v, got %v", key, expected, kv.Value)
			}
			return
		}
	}
	t.Errorf("missing configuration %s", key)
}

// SetAgentlessEndpoint is used for testing purposes to replace the real agentless
// endpoint with a custom one
func SetAgentlessEndpoint(endpoint string) string {
	agentlessEndpointLock.Lock()
	defer agentlessEndpointLock.Unlock()
	prev := agentlessURL
	agentlessURL = endpoint
	return prev
}
