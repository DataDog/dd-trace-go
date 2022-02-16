package telemetry_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

func TestClient(t *testing.T) {
	ch := make(chan telemetry.RequestType)
	var gotheartbeat int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("DD-Telemetry-Request-Type")
		if len(h) == 0 {
			t.Fatal("didn't get telemetry request type header")
		}
		if telemetry.RequestType(h) == telemetry.RequestTypeAppHeartbeat {
			// only keep the first heartbeat in case we happen to get
			// multiple heartbeats in the waiting interval to avoid flaky
			// tests
			if !atomic.CompareAndSwapInt64(&gotheartbeat, 0, 1) {
				return
			}
		}
		ch <- telemetry.RequestType(h)
	}))
	defer server.Close()

	go func() {
		client := &telemetry.Client{
			URL:                server.URL,
			SubmissionInterval: 5 * time.Millisecond,
		}
		client.Start(nil, nil)
		client.Start(nil, nil) // test idempotence
		// Give the submission interval time to pass so we
		// can get a heartbeat.
		time.Sleep(10 * time.Millisecond)
		client.Stop()
		client.Stop() // test idempotence
	}()

	// TODO: Could this wait time be a source of flakiness? Should it be
	// longer?
	wait := time.NewTimer(100 * time.Millisecond)
	defer wait.Stop()

	var got []telemetry.RequestType
	for i := 0; i < 3; i++ {
		select {
		case <-wait.C:
			t.Fatal("timed out waiting for server to get request")
		case header := <-ch:
			got = append(got, header)
		}
	}

	want := []telemetry.RequestType{telemetry.RequestTypeAppStarted, telemetry.RequestTypeAppHeartbeat, telemetry.RequestTypeAppClosing}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("wanted %v, got %v", want, got)
	}
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

	select {
	case <-closed:
	case <-time.NewTimer(100 * time.Millisecond).C:
		t.Fatalf("timed out waiting for requests to complete")
	}

	want := []telemetry.Series{
		{Name: "baz", Type: "count", Points: [][2]float64{{0, 4}}, Common: true},
		{Name: "bonk", Type: "count", Points: [][2]float64{{0, 4}}, Tags: []string{"org:1"}},
		{Name: "foobar", Type: "gauge", Points: [][2]float64{{0, 2}}},
	}
	sort.Slice(got, func(i, j int) bool {
		return got[i].Name < got[j].Name
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
	client.Flush()
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
	client.Flush()
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

	select {
	case <-closed:
	case <-time.NewTimer(500 * time.Millisecond).C:
		t.Fatal("test tiemd out waiting for all messages to send")
	}
	want := []telemetry.Series{
		{Name: "foobar", Type: "count", Points: [][2]float64{{0, 80}}, Tags: []string{"tag"}},
	}
	sort.Slice(got, func(i, j int) bool {
		return got[i].Name < got[j].Name
	})
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("want %+v, got %+v", want, got)
	}
}
