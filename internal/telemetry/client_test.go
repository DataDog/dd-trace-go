// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
)

func TestClient(t *testing.T) {
	t.Setenv("DD_TELEMETRY_HEARTBEAT_INTERVAL", "1")
	heartbeat := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("DD-Telemetry-Request-Type")
		if len(h) == 0 {
			t.Fatal("didn't get telemetry request type header")
		}
		if RequestType(h) == RequestTypeAppHeartbeat {
			select {
			case heartbeat <- struct{}{}:
			default:
			}
		}
	}))
	defer server.Close()

	client := &client{
		URL: server.URL,
	}
	client.Start(nil)
	client.Start(nil) // test idempotence
	defer client.Stop()

	timeout := time.After(30 * time.Second)
	select {
	case <-timeout:
		t.Fatal("Heartbeat took more than 30 seconds. Should have been ~1 second")
	case <-heartbeat:
	}

}

func TestMetrics(t *testing.T) {
	t.Setenv("DD_TELEMETRY_HEARTBEAT_INTERVAL", "1")
	var (
		mu  sync.Mutex
		got []Series
	)
	closed := make(chan struct{}, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("DD-Telemetry-Request-Type") == string(RequestTypeAppClosing) {
			select {
			case closed <- struct{}{}:
			default:
			}
			return
		}
		req := Body{
			Payload: new(Metrics),
		}
		dec := json.NewDecoder(r.Body)
		err := dec.Decode(&req)
		if err != nil {
			t.Fatal(err)
		}
		if req.RequestType != RequestTypeGenerateMetrics {
			return
		}
		v, ok := req.Payload.(*Metrics)
		if !ok {
			t.Fatal("payload set metrics but didn't get metrics")
		}
		for _, s := range v.Series {
			for i, p := range s.Points {
				// zero out timestamps
				s.Points[i] = [2]float64{0, p[1]}
			}
		}
		mu.Lock()
		got = append(got, v.Series...)
		mu.Unlock()
	}))
	defer server.Close()

	go func() {
		client := &client{
			URL: server.URL,
		}
		client.Start(nil)

		// Gauges should have the most recent value
		client.Gauge(NamespaceTracers, "foobar", 1, nil, false)
		client.Gauge(NamespaceTracers, "foobar", 2, nil, false)
		// Counts should be aggregated
		client.Count(NamespaceTracers, "baz", 3, nil, true)
		client.Count(NamespaceTracers, "baz", 1, nil, true)
		// Tags should be passed through
		client.Count(NamespaceTracers, "bonk", 4, []string{"org:1"}, false)
		client.Stop()
	}()

	<-closed

	want := []Series{
		{Metric: "baz", Type: "count", Points: [][2]float64{{0, 4}}, Tags: []string{}, Common: true},
		{Metric: "bonk", Type: "count", Points: [][2]float64{{0, 4}}, Tags: []string{"org:1"}},
		{Metric: "foobar", Type: "gauge", Points: [][2]float64{{0, 2}}, Tags: []string{}},
	}
	sort.Slice(got, func(i, j int) bool {
		return got[i].Metric < got[j].Metric
	})
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("want %+v, got %+v", want, got)
	}
}

func TestDisabledClient(t *testing.T) {
	t.Setenv("DD_TELEMETRY_HEARTBEAT_INTERVAL", "1")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "0")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("shouldn't have got any requests")
	}))
	defer server.Close()

	client := &client{
		URL: server.URL,
	}
	client.Start(nil)
	client.Gauge(NamespaceTracers, "foobar", 1, nil, false)
	client.Count(NamespaceTracers, "bonk", 4, []string{"org:1"}, false)
	client.Stop()
}

func TestNonStartedClient(t *testing.T) {
	t.Setenv("DD_TELEMETRY_HEARTBEAT_INTERVAL", "1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("shouldn't have got any requests")
	}))
	defer server.Close()

	client := &client{
		URL: server.URL,
	}
	client.Gauge(NamespaceTracers, "foobar", 1, nil, false)
	client.Count(NamespaceTracers, "bonk", 4, []string{"org:1"}, false)
	client.Stop()
}

func TestConcurrentClient(t *testing.T) {
	t.Setenv("DD_TELEMETRY_HEARTBEAT_INTERVAL", "1")
	var (
		mu  sync.Mutex
		got []Series
	)
	closed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Log("foo")
		if r.Header.Get("DD-Telemetry-Request-Type") == string(RequestTypeAppClosing) {
			select {
			case closed <- struct{}{}:
			default:
				return
			}
		}
		req := Body{
			Payload: new(Metrics),
		}
		dec := json.NewDecoder(r.Body)
		err := dec.Decode(&req)
		if err != nil {
			t.Fatal(err)
		}
		if req.RequestType != RequestTypeGenerateMetrics {
			return
		}
		v, ok := req.Payload.(*Metrics)
		if !ok {
			t.Fatal("payload set metrics but didn't get metrics")
		}
		for _, s := range v.Series {
			for i, p := range s.Points {
				// zero out timestamps
				s.Points[i] = [2]float64{0, p[1]}
			}
		}
		mu.Lock()
		got = append(got, v.Series...)
		mu.Unlock()
	}))
	defer server.Close()

	go func() {
		client := &client{
			URL: server.URL,
		}
		client.Start(nil)
		defer client.Stop()

		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 10; j++ {
					client.Count(NamespaceTracers, "foobar", 1, []string{"tag"}, false)
				}
			}()
		}
		wg.Wait()
	}()

	<-closed

	want := []Series{
		{Metric: "foobar", Type: "count", Points: [][2]float64{{0, 80}}, Tags: []string{"tag"}},
	}
	sort.Slice(got, func(i, j int) bool {
		return got[i].Metric < got[j].Metric
	})
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("want %+v, got %+v", want, got)
	}
}

// fakeAgentless is a helper function for TestAgentlessRetry. It replaces the agentless
// endpoint in the telemetry package with a custom server URL and returns
//  1. a function that waits for a telemetry request to that server
//  2. a cleanup function that closes the server and resets the agentless endpoint to
//     its original value
func fakeAgentless(ctx context.Context, t *testing.T) (wait func(), cleanup func()) {
	received := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("DD-Telemetry-Request-Type")
		if len(h) > 0 {
			received <- struct{}{}
		}
	}))
	prevEndpoint := SetAgentlessEndpoint(server.URL)
	return func() {
			select {
			case <-ctx.Done():
				t.Fatalf("fake agentless endpoint timed out waiting for telemetry")
			case <-received:
				return
			}
		}, func() {
			server.Close()
			SetAgentlessEndpoint(prevEndpoint)
		}
}

// TestAgentlessRetry tests the behavior of the telemetry client in the case where
// the client cannot connect to the agent. The client should re-try the request
// with the agentless endpoint.
func TestAgentlessRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	waitAgentlessEndpoint, cleanup := fakeAgentless(ctx, t)
	defer cleanup()

	brokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	}))
	brokenServer.Close()

	client := &client{
		URL: brokenServer.URL,
	}
	client.Start(nil)
	waitAgentlessEndpoint()
}

func TestCollectDependencies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	received := make(chan *Dependencies)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("DD-Telemetry-Request-Type") == string(RequestTypeDependenciesLoaded) {
			var body Body
			body.Payload = new(Dependencies)
			err := json.NewDecoder(r.Body).Decode(&body)
			if err != nil {
				t.Errorf("bad body: %s", err)
			}
			select {
			case received <- body.Payload.(*Dependencies):
			default:
			}
		}
	}))
	defer server.Close()
	client := &client{
		URL: server.URL,
	}
	client.Start(nil)
	select {
	case <-received:
	case <-ctx.Done():
		t.Fatalf("Timed out waiting for dependency payload")
	}
}
