// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

func TestMain(m *testing.M) {
	// Profiling configuration is logged by default when starting a profile,
	// so we want to discard it during tests to avoid flooding the terminal
	// with logs
	log.UseLogger(log.DiscardLogger{})
	os.Exit(m.Run())
}

func TestStart(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		rl := &log.RecordLogger{}
		defer log.UseLogger(rl)()

		if err := Start(); err != nil {
			t.Fatal(err)
		}
		defer Stop()

		// Profiler configuration should be logged by default.  Check
		// that we log some default configuration, e.g. enabled profiles
		assert.LessOrEqual(t, 1, len(rl.Logs()))
		startupLog := strings.Join(rl.Logs(), " ")
		assert.Contains(t, startupLog, "cpu")
		assert.Contains(t, startupLog, "heap")

		mu.Lock()
		require.NotNil(t, activeProfiler)
		assert := assert.New(t)
		if host, err := os.Hostname(); err != nil {
			assert.Equal(host, activeProfiler.cfg.hostname)
		}
		assert.Equal("http://"+net.JoinHostPort(defaultAgentHost, defaultAgentPort)+"/profiling/v1/input",
			activeProfiler.cfg.agentURL)
		assert.Equal(defaultAPIURL, activeProfiler.cfg.apiURL)
		assert.Equal(activeProfiler.cfg.agentURL, activeProfiler.cfg.targetURL)
		assert.Equal(DefaultPeriod, activeProfiler.cfg.period)
		assert.Equal(len(defaultProfileTypes), len(activeProfiler.cfg.types))
		for _, pt := range defaultProfileTypes {
			_, ok := activeProfiler.cfg.types[pt]
			assert.True(ok)
		}
		assert.Equal(DefaultDuration, activeProfiler.cfg.cpuDuration)
		mu.Unlock()
	})

	t.Run("options", func(t *testing.T) {
		if err := Start(); err != nil {
			t.Fatal(err)
		}
		defer Stop()

		mu.Lock()
		require.NotNil(t, activeProfiler)
		assert.NotEmpty(t, activeProfiler.cfg.hostname)
		mu.Unlock()
	})

	t.Run("options/GoodAPIKey/Agent", func(t *testing.T) {
		rl := &log.RecordLogger{}
		defer log.UseLogger(rl)()

		err := Start(WithAPIKey("12345678901234567890123456789012"))
		defer Stop()
		assert.Nil(t, err)
		assert.Equal(t, activeProfiler.cfg.agentURL, activeProfiler.cfg.targetURL)
		// The package should log a warning that using an API has no
		// effect unless uploading directly to Datadog (i.e. agentless)
		assert.LessOrEqual(t, 1, len(rl.Logs()))
		assert.Contains(t, strings.Join(rl.Logs(), " "), "profiler.WithAPIKey")
	})

	t.Run("options/GoodAPIKey/Agentless", func(t *testing.T) {
		rl := &log.RecordLogger{}
		defer log.UseLogger(rl)()

		err := Start(
			WithAPIKey("12345678901234567890123456789012"),
			WithAgentlessUpload(),
		)
		defer Stop()
		assert.Nil(t, err)
		assert.Equal(t, activeProfiler.cfg.apiURL, activeProfiler.cfg.targetURL)
		// The package should log a warning that agentless upload is not
		// officially supported, so prefer not to use it
		assert.LessOrEqual(t, 1, len(rl.Logs()))
		assert.Contains(t, strings.Join(rl.Logs(), " "), "profiler.WithAgentlessUpload")
	})

	t.Run("options/BadAPIKey", func(t *testing.T) {
		err := Start(WithAPIKey("aaaa"), WithAgentlessUpload())
		defer Stop()
		assert.NotNil(t, err)

		// Check that mu gets unlocked, even if newProfiler() returns an error.
		mu.Lock()
		mu.Unlock()
	})
}

func TestStartStopIdempotency(t *testing.T) {
	t.Run("linear", func(t *testing.T) {
		Start(WithProfileTypes())
		Start(WithProfileTypes())
		Start(WithProfileTypes())
		Start(WithProfileTypes())
		Start(WithProfileTypes())
		Start(WithProfileTypes())

		Stop()
		Stop()
		Stop()
		Stop()
		Stop()
	})

	t.Run("parallel", func(t *testing.T) {
		var wg sync.WaitGroup

		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 20; j++ {
					// startup logging makes this test very slow
					//
					// Also, the CPU profile is really slow
					// to stop (200ms/iter) and in general
					// we don't need to actually run any
					// profiles for this test
					Start(WithLogStartup(false), WithProfileTypes())
				}
			}()
		}
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 20; j++ {
					Stop()
				}
			}()
		}
		wg.Wait()
	})

	t.Run("stop", func(t *testing.T) {
		Start(WithPeriod(time.Minute))
		defer Stop()

		mu.Lock()
		require.NotNil(t, activeProfiler)
		activeProfiler.stop()
		activeProfiler.stop()
		activeProfiler.stop()
		activeProfiler.stop()
		mu.Unlock()
	})
}

// TestStopLatency tries to make sure that calling Stop() doesn't hang, i.e.
// that ongoing profiling or upload operations are immediately canceled.
func TestStopLatency(t *testing.T) {
	received := make(chan struct{})
	stop := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case received <- struct{}{}:
		default:
		}
		<-stop
	}))
	defer server.Close()
	defer close(stop)

	Start(
		WithAgentAddr(server.Listener.Addr().String()),
		WithPeriod(time.Second),
		WithUploadTimeout(time.Hour),
	)

	<-received
	// received indicates that an upload has started, and due to waiting on
	// stop the upload won't actually complete. So we know calling Stop()
	// should interrupt the upload. Ideally profiling is also currently
	// running, though that is harder to detect and guarantee.
	start := time.Now()
	Stop()

	// CPU profiling can take up to 200ms to stop. This is because the inner
	// loop of CPU profiling has an uninterruptible 100ms sleep. Each
	// iteration reads available profile samples, so it can take two
	// iterations to stop: one to read the remaining samples, and a second
	// to detect that there are no more samples and exit. We give extra time
	// on top of that to account for CI slowness.
	elapsed := time.Since(start)
	if elapsed > 500*time.Millisecond {
		t.Errorf("profiler took %v to stop", elapsed)
	}
}

func TestSetProfileFraction(t *testing.T) {
	t.Run("on", func(t *testing.T) {
		start := runtime.SetMutexProfileFraction(0)
		defer runtime.SetMutexProfileFraction(start)
		p, err := unstartedProfiler(WithProfileTypes(MutexProfile))
		require.NoError(t, err)
		p.run()
		p.stop()
		assert.Equal(t, DefaultMutexFraction, runtime.SetMutexProfileFraction(-1))
	})

	t.Run("off", func(t *testing.T) {
		start := runtime.SetMutexProfileFraction(0)
		defer runtime.SetMutexProfileFraction(start)
		p, err := unstartedProfiler()
		require.NoError(t, err)
		p.run()
		p.stop()
		assert.Zero(t, runtime.SetMutexProfileFraction(-1))
	})
}

func TestProfilerPassthrough(t *testing.T) {
	if testing.Short() {
		return
	}
	out := make(chan batch)
	p, err := newProfiler()
	require.NoError(t, err)
	p.cfg.period = 200 * time.Millisecond
	p.cfg.cpuDuration = 1 * time.Millisecond
	p.uploadFunc = func(bat batch) error {
		out <- bat
		return nil
	}
	p.run()
	defer p.stop()
	var bat batch
	select {
	case bat = <-out:
	// TODO (knusbaum) this timeout is long because we were seeing timeouts at 500ms.
	// it would be nice to have a time-independent way to test this
	case <-time.After(1000 * time.Millisecond):
		t.Fatal("time expired")
	}

	assert := assert.New(t)
	// should contain cpu.pprof, delta-heap.pprof
	assert.Equal(2, len(bat.profiles))
	assert.NotEmpty(bat.profiles[0].data)
	assert.NotEmpty(bat.profiles[1].data)
}

func unstartedProfiler(opts ...Option) (*profiler, error) {
	p, err := newProfiler(opts...)
	if err != nil {
		return nil, err
	}
	p.uploadFunc = func(_ batch) error { return nil }
	return p, nil
}

type profileMeta struct {
	tags        []string
	headers     http.Header
	event       uploadEvent
	attachments map[string][]byte
}

type mockBackend struct {
	t        *testing.T
	profiles chan profileMeta
}

func (m *mockBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	profile := profileMeta{
		attachments: make(map[string][]byte),
	}
	defer func() {
		select {
		case m.profiles <- profile:
		default:
		}
	}()
	profile.headers = r.Header.Clone()
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		m.t.Fatalf("bad multipart form: %s", err)
		return
	}
	file, _, err := r.FormFile("event")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		m.t.Fatalf("getting event.json: %s", err)
		return
	}
	if err := json.NewDecoder(file).Decode(&profile.event); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		m.t.Fatalf("decoding event payload: %s", err)
		return
	}

	profile.tags = append(profile.tags, strings.Split(profile.event.Tags, ",")...)
	for _, name := range profile.event.Attachments {
		f, _, err := r.FormFile(name)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			m.t.Fatalf("event attachment %s is missing from upload: %s", name, err)
			return
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			m.t.Fatalf("reading attachment %s: %s", name, err)
			return
		}
		profile.attachments[name] = data
	}
	w.WriteHeader(http.StatusAccepted)
}

func TestAllUploaded(t *testing.T) {
	// This is a kind of end-to-end test that runs the real profiles (i.e.
	// not mocking/replacing any internal functions) and verifies that the
	// profiles are at least uploaded.
	//
	// TODO: Further check that the uploaded profiles are all valid

	// received indicates that the server has received a profile upload.
	// This is used similarly to a sync.WaitGroup but avoids a potential
	// panic if too many requests are received before profiling is stopped
	// and the WaitGroup count goes negative.
	//
	// The channel is buffered with 2 entries so we can check that the
	// second batch of profiles is correct in case the profiler gets in a
	// bad state after the first round of profiling.
	received := make(chan profileMeta, 2)
	server := httptest.NewServer(&mockBackend{t: t, profiles: received})
	defer server.Close()

	t.Setenv("DD_PROFILING_WAIT_PROFILE", "1")
	Start(
		WithAgentAddr(server.Listener.Addr().String()),
		WithProfileTypes(
			BlockProfile,
			CPUProfile,
			GoroutineProfile,
			HeapProfile,
			MutexProfile,
		),
		WithPeriod(10*time.Millisecond),
		CPUDuration(1*time.Millisecond),
	)
	defer Stop()

	validateProfile := func(profile profileMeta, seq uint64) {
		expected := []string{
			"cpu.pprof",
			"delta-block.pprof",
			"delta-heap.pprof",
			"delta-mutex.pprof",
			"goroutines.pprof",
			"goroutineswait.pprof",
		}
		assert.ElementsMatch(t, expected, profile.event.Attachments)

		assert.Contains(t, profile.tags, fmt.Sprintf("profile_seq:%d", seq))
	}

	validateProfile(<-received, 0)
	validateProfile(<-received, 1)
}

func TestCorrectTags(t *testing.T) {
	got := make(chan profileMeta)
	server := httptest.NewServer(&mockBackend{t: t, profiles: got})
	defer server.Close()

	Start(
		WithAgentAddr(server.Listener.Addr().String()),
		WithProfileTypes(
			HeapProfile,
		),
		WithPeriod(10*time.Millisecond),
		WithService("xyz"),
		WithEnv("testing"),
		WithTags("foo:bar", "baz:bonk"),
		WithHostname("example"),
	)
	defer Stop()
	expected := []string{
		"baz:bonk",
		"env:testing",
		"foo:bar",
		"service:xyz",
		"host:example",
	}
	for i := 0; i < 20; i++ {
		// We check the tags we get several times to try to have a
		// better chance of catching a bug where the some of the tags
		// are clobbered due to a bug caused by the same
		// profiler-internal tag slice being appended to from different
		// goroutines concurrently.
		p := <-got
		for _, tag := range expected {
			require.Contains(t, p.tags, tag)
		}
	}
}

func TestTelemetryEnabled(t *testing.T) {
	received := make(chan *telemetry.AppStarted, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/telemetry/proxy/api/v2/apmtelemetry" {
			return
		}
		if r.Header.Get("DD-Telemetry-Request-Type") != string(telemetry.RequestTypeAppStarted) {
			return
		}

		var body telemetry.Request
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

	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
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
	payload := <-received
	check := func(key string, expected interface{}) {
		for _, kv := range payload.Configuration {
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

func TestImmediateProfile(t *testing.T) {
	received := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case received <- struct{}{}:
		default:
		}
	}))
	defer server.Close()

	err := Start(
		WithAgentAddr(server.Listener.Addr().String()),
		WithProfileTypes(HeapProfile),
		WithPeriod(3*time.Second),
	)
	require.NoError(t, err)
	defer Stop()

	// Wait a little less than 2 profile periods. We should start profiling
	// immediately. If it takes significantly longer than 1 profile period to get
	// a profile, consider the test failed
	timeout := time.After(5 * time.Second)
	select {
	case <-timeout:
		t.Fatal("should have received a profile already")
	case <-received:
	}
}
