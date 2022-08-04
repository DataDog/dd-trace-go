// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package telemetry_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

func TestClient(t *testing.T) {
	heartbeat := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("DD-Telemetry-Request-Type")
		if len(h) == 0 {
			t.Fatal("didn't get telemetry request type header")
		}
		if telemetry.RequestType(h) == telemetry.RequestTypeAppHeartbeat {
			select {
			case heartbeat <- struct{}{}:
			default:
			}
		}
	}))
	defer server.Close()

	client := &telemetry.Client{
		URL:                server.URL,
		SubmissionInterval: time.Millisecond,
	}
	client.Start(nil, nil)
	client.Start(nil, nil) // test idempotence
	defer client.Stop()

	<-heartbeat
}

func TestMetrics(t *testing.T) {
	var (
		mu  sync.Mutex
		got []telemetry.Series
	)
	closed := make(chan struct{}, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("DD-Telemetry-Request-Type") == string(telemetry.RequestTypeAppClosing) {
			select {
			case closed <- struct{}{}:
			default:
			}
			return
		}
		req := telemetry.Request{
			Payload: new(telemetry.Metrics),
		}
		dec := json.NewDecoder(r.Body)
		err := dec.Decode(&req)
		if err != nil {
			t.Fatal(err)
		}
		if req.RequestType != telemetry.RequestTypeGenerateMetrics {
			return
		}
		v, ok := req.Payload.(*telemetry.Metrics)
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
		client := &telemetry.Client{
			URL: server.URL,
		}
		client.Start(nil, nil)

		// Gauges should have the most recent value
		client.Gauge("foobar", 1, nil, false)
		client.Gauge("foobar", 2, nil, false)
		// Counts should be aggregated
		client.Count("baz", 3, nil, true)
		client.Count("baz", 1, nil, true)
		// Tags should be passed through
		client.Count("bonk", 4, []string{"org:1"}, false)
		client.Stop()
	}()

	<-closed

	want := []telemetry.Series{
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

// testSetEnv is a copy of testing.T.Setenv so we can build this library
// for Go versions prior to 1.17
func testSetEnv(t *testing.T, key, val string) {
	prev, ok := os.LookupEnv(key)
	if ok {
		t.Cleanup(func() { os.Setenv(key, prev) })
	} else {
		t.Cleanup(func() { os.Unsetenv(key) })
	}
	os.Setenv(key, val)
}

func TestDisabledClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("shouldn't have got any requests")
	}))
	defer server.Close()
	testSetEnv(t, "DD_INSTRUMENTATION_TELEMETRY_ENABLED", "0")

	client := &telemetry.Client{
		URL:                server.URL,
		SubmissionInterval: time.Millisecond,
	}
	client.Start(nil, nil)
	client.Gauge("foobar", 1, nil, false)
	client.Count("bonk", 4, []string{"org:1"}, false)
	client.Stop()
}

func TestNonStartedClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("shouldn't have got any requests")
	}))
	defer server.Close()

	client := &telemetry.Client{
		URL:                server.URL,
		SubmissionInterval: time.Millisecond,
	}
	client.Gauge("foobar", 1, nil, false)
	client.Count("bonk", 4, []string{"org:1"}, false)
	client.Stop()
}

func TestConcurrentClient(t *testing.T) {
	var (
		mu  sync.Mutex
		got []telemetry.Series
	)
	closed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Log("foo")
		if r.Header.Get("DD-Telemetry-Request-Type") == string(telemetry.RequestTypeAppClosing) {
			select {
			case closed <- struct{}{}:
			default:
				return
			}
		}
		req := telemetry.Request{
			Payload: new(telemetry.Metrics),
		}
		dec := json.NewDecoder(r.Body)
		err := dec.Decode(&req)
		if err != nil {
			t.Fatal(err)
		}
		if req.RequestType != telemetry.RequestTypeGenerateMetrics {
			return
		}
		v, ok := req.Payload.(*telemetry.Metrics)
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
		client := &telemetry.Client{
			URL: server.URL,
		}
		client.Start(nil, nil)
		defer client.Stop()

		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 10; j++ {
					client.Count("foobar", 1, []string{"tag"}, false)
				}
			}()
		}
		wg.Wait()
	}()

	<-closed

	want := []telemetry.Series{
		{Metric: "foobar", Type: "count", Points: [][2]float64{{0, 80}}, Tags: []string{"tag"}},
	}
	sort.Slice(got, func(i, j int) bool {
		return got[i].Metric < got[j].Metric
	})
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("want %+v, got %+v", want, got)
	}
}
