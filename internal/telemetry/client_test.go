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

	"github.com/stretchr/testify/assert"
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
	client.mu.Lock()
	client.start(nil, NamespaceTracers, true)
	client.start(nil, NamespaceTracers, true) // test idempotence
	client.mu.Unlock()
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

	// we will try to set three metrics that the server must receive
	expectedMetrics := 3

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rType := RequestType(r.Header.Get("DD-Telemetry-Request-Type"))
		if rType != RequestTypeGenerateMetrics {
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
		defer mu.Unlock()
		got = append(got, v.Series...)
		if len(got) == expectedMetrics {
			select {
			case closed <- struct{}{}:
			default:
			}
			return
		}
	}))
	defer server.Close()

	go func() {
		client := &client{
			URL: server.URL,
		}
		client.start(nil, NamespaceTracers, true)

		// Records should have the most recent value
		client.Record(NamespaceTracers, MetricKindGauge, "foobar", 1, nil, false)
		client.Record(NamespaceTracers, MetricKindGauge, "foobar", 2, nil, false)
		// Counts should be aggregated
		client.Count(NamespaceTracers, "baz", 3, nil, true)
		client.Count(NamespaceTracers, "baz", 1, nil, true)
		// Tags should be passed through
		client.Count(NamespaceTracers, "bonk", 4, []string{"org:1"}, false)

		client.mu.Lock()
		client.flush()
		client.mu.Unlock()
	}()

	<-closed

	want := []Series{
		{Metric: "baz", Type: "count", Interval: 0, Points: [][2]float64{{0, 4}}, Tags: []string{}, Common: true},
		{Metric: "bonk", Type: "count", Interval: 0, Points: [][2]float64{{0, 4}}, Tags: []string{"org:1"}},
		{Metric: "foobar", Type: "gauge", Interval: 0, Points: [][2]float64{{0, 2}}, Tags: []string{}},
	}
	sort.Slice(got, func(i, j int) bool {
		return got[i].Metric < got[j].Metric
	})
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("want %+v, got %+v", want, got)
	}
}

func TestDistributionMetrics(t *testing.T) {
	t.Setenv("DD_TELEMETRY_HEARTBEAT_INTERVAL", "1")
	var (
		mu  sync.Mutex
		got []DistributionSeries
	)
	closed := make(chan struct{}, 1)

	// we will try to set one metric that the server must receive
	expectedMetrics := 1

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rType := RequestType(r.Header.Get("DD-Telemetry-Request-Type"))
		if rType != RequestTypeDistributions {
			return
		}
		req := Body{
			Payload: new(DistributionMetrics),
		}
		dec := json.NewDecoder(r.Body)
		err := dec.Decode(&req)
		if err != nil {
			t.Fatal(err)
		}
		v, ok := req.Payload.(*DistributionMetrics)
		if !ok {
			t.Fatal("payload set metrics but didn't get metrics")
		}
		mu.Lock()
		defer mu.Unlock()
		got = append(got, v.Series...)
		if len(got) == expectedMetrics {
			select {
			case closed <- struct{}{}:
			default:
			}
			return
		}
	}))
	defer server.Close()

	go func() {
		client := &client{
			URL: server.URL,
		}
		client.start(nil, NamespaceTracers, true)
		// Records should have the most recent value
		client.Record(NamespaceTracers, MetricKindDist, "soobar", 1, nil, false)
		client.Record(NamespaceTracers, MetricKindDist, "soobar", 3, nil, false)
		client.mu.Lock()
		client.flush()
		client.mu.Unlock()
	}()

	<-closed

	want := []DistributionSeries{
		// Distributions do not record metric types since it is its own event
		{Metric: "soobar", Points: []float64{3}, Tags: []string{}},
	}
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
	client.start(nil, NamespaceTracers, true)
	client.Record(NamespaceTracers, MetricKindGauge, "foobar", 1, nil, false)
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
	client.Record(NamespaceTracers, MetricKindGauge, "foobar", 1, nil, false)
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
		defer mu.Unlock()
		got = append(got, v.Series...)
		select {
		case closed <- struct{}{}:
		default:
			return
		}
	}))
	defer server.Close()

	go func() {
		client := &client{
			URL: server.URL,
		}
		client.start(nil, NamespaceTracers, true)
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
	received := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("DD-Telemetry-Request-Type") == string(RequestTypeAppStarted) {
			select {
			case received <- struct{}{}:
			default:
			}
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
	client.start(nil, NamespaceTracers, true)
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
	client.start(nil, NamespaceTracers, true)
	select {
	case <-received:
	case <-ctx.Done():
		t.Fatalf("Timed out waiting for dependency payload")
	}
}

func Test_heartbeatInterval(t *testing.T) {
	defaultInterval := time.Second * time.Duration(defaultHeartbeatInterval)
	tests := []struct {
		name  string
		setup func(t *testing.T)
		want  time.Duration
	}{
		{
			name:  "default",
			setup: func(t *testing.T) {},
			want:  defaultInterval,
		},
		{
			name:  "float",
			setup: func(t *testing.T) { t.Setenv("DD_TELEMETRY_HEARTBEAT_INTERVAL", "0.2") },
			want:  time.Millisecond * 200,
		},
		{
			name:  "integer",
			setup: func(t *testing.T) { t.Setenv("DD_TELEMETRY_HEARTBEAT_INTERVAL", "2") },
			want:  time.Second * 2,
		},
		{
			name:  "negative",
			setup: func(t *testing.T) { t.Setenv("DD_TELEMETRY_HEARTBEAT_INTERVAL", "-1") },
			want:  defaultInterval,
		},
		{
			name:  "zero",
			setup: func(t *testing.T) { t.Setenv("DD_TELEMETRY_HEARTBEAT_INTERVAL", "0") },
			want:  defaultInterval,
		},
		{
			name:  "long",
			setup: func(t *testing.T) { t.Setenv("DD_TELEMETRY_HEARTBEAT_INTERVAL", "4000") },
			want:  defaultInterval,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup(t)
			assert.Equal(t, tt.want, heartbeatInterval())
		})
	}
}

func TestNoEmptyHeaders(t *testing.T) {
	c := &client{}
	req := c.newRequest(RequestTypeAppStarted)
	assertNotEmpty := func(header string) {
		headers := *req.Header
		vals := headers[header]
		for _, v := range vals {
			assert.NotEmpty(t, v, "%s header should not be empty", header)
		}
	}
	assertNotEmpty("Datadog-Container-ID")
	assertNotEmpty("Datadog-Entity-ID")
}
