// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

func TestTelemetryEnabled(t *testing.T) {
	t.Setenv("DD_TRACE_STARTUP_LOGS", "0")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	received := make(chan *telemetry.AppStarted, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/telemetry/proxy/api/v2/apmtelemetry" {
			return
		}
		if r.Header.Get("DD-Telemetry-Request-Type") != string(telemetry.RequestTypeAppStarted) {
			return
		}
		var body telemetry.Body
		body.Payload = new(telemetry.AppStarted)
		err := json.NewDecoder(r.Body).Decode(&body)
		if err != nil {
			t.Errorf("bad body: %s", err)
		}
		select {
		case received <- body.Payload.(*telemetry.AppStarted):
		default:
		}
	}))
	defer server.Close()

	Start(
		WithAgentAddr(server.Listener.Addr().String()),
		WithDebugStack(false),
		WithService("test-serv"),
		WithEnv("test-env"),
		WithRuntimeMetrics(),
	)
	defer Stop()

	var payload *telemetry.AppStarted
	select {
	case <-ctx.Done():
		t.Fatalf("Time out: waiting for telemetry payload")
	case payload = <-received:
	}

	check := func(key string, expected interface{}) {
		for _, kv := range payload.Configuration {
			if kv.Name == key {
				if kv.Value != expected {
					t.Errorf("configuration %s: wanted %v, got %v", key, expected, kv.Value)
				}
				return
			}
		}
		t.Errorf("missing configuration %s", key)
	}

	check("trace_debug_enabled", false)
	check("service", "test-serv")
	check("env", "test-env")
	check("runtime_metrics_enabled", true)
}
