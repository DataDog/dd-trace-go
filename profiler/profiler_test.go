// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"encoding/json"
	"io"
	"io/ioutil"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	pprofile "github.com/google/pprof/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		Start()
		Start()
		Start()
		Start()
		Start()
		Start()

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
				for j := 0; j < 1000; j++ {
					// startup logging makes this test very slow
					Start(WithLogStartup(false))
				}
			}()
		}
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 1000; j++ {
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
	p, err := newProfiler(
		WithURL("http://invalid.invalid/"),
		WithPeriod(1000*time.Millisecond),
		CPUDuration(500*time.Millisecond),
	)
	require.NoError(t, err)
	uploadStart := make(chan struct{}, 1)
	uploadFunc := p.uploadFunc
	p.uploadFunc = func(b batch) error {
		select {
		case uploadStart <- struct{}{}:
		default:
			// uploadFunc may be called more than once, don't leak this goroutine
		}
		return uploadFunc(b)
	}
	p.run()

	<-uploadStart
	// Wait for uploadFunc(b) to run. A bit racy, but worst case is the test
	// passing for the wrong reasons.
	time.Sleep(10 * time.Millisecond)

	stopped := make(chan struct{}, 1)
	go func() {
		p.stop()
		stopped <- struct{}{}
	}()

	// CPU profiling polls in 100 millisecond intervals and this can't be
	// interrupted by pprof.StopCPUProfile, so we can't guarantee profiling
	// will stop faster than that.
	timeout := 300 * time.Millisecond
	select {
	case <-stopped:
	case <-time.After(timeout):
		// Capture stacks so we can see which goroutines are hanging and why.
		stacks := make([]byte, 64*1024)
		stacks = stacks[0:runtime.Stack(stacks, true)]
		t.Fatalf("Stop() took longer than %s:\n%s", timeout, stacks)
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

func unstartedProfiler(opts ...Option) (*profiler, error) {
	p, err := newProfiler(opts...)
	if err != nil {
		return nil, err
	}
	p.uploadFunc = func(_ batch) error { return nil }
	return p, nil
}

func TestAllUploaded(t *testing.T) {
	// This is a kind of end-to-end test that runs the real profiles (i.e.
	// not mocking/replacing any internal functions) and verifies that the
	// profiles are at least uploaded.

	// received indicates that the server has received a profile upload.
	// This is used similarly to a sync.WaitGroup but avoids a potential
	// panic if too many requests are received before profiling is stopped
	// and the WaitGroup count goes negative.
	//
	// The channel is buffered with 2 entries so we can check that the
	// second batch of profiles is correct in case the profiler gets in a
	// bad state after the first round of profiling.
	received := make(chan []string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var profiles []string
		defer func() {
			select {
			case received <- profiles:
			default:
			}
		}()
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("bad media type: %s", err)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				return
			}
			if err != nil {
				t.Fatalf("next part: %s", err)
			}
			if strings.Contains(p.FormName(), "pprof") && p.FileName() == "pprof-data" {
				prof, err := pprofile.Parse(p)
				if err != nil {
					t.Fatalf("parsing pprof: %s", err)
				}
				err = prof.CheckValid()
				if err != nil {
					t.Fatalf("invalid pprof: %s", err)
				}
				profiles = append(profiles, p.FormName())
			}
			if strings.Contains(p.FormName(), "json") && p.FileName() == "pprof-data" {
				data, err := ioutil.ReadAll(p)
				if err != nil {
					t.Fatalf("reading form data: %s", err)
				}
				if !json.Valid(data) {
					t.Fatalf("metrics JSON invalid: %s", data)
				}
				profiles = append(profiles, p.FormName())
			}
		}
	}))
	defer server.Close()

	testSetenv(t, "DD_PROFILING_WAIT_PROFILE", "1")
	period := 10 * time.Millisecond
	cpuDuration := time.Millisecond
	if !testing.Short() {
		period = 1 * time.Second
		cpuDuration = 1 * time.Second
	}
	Start(
		WithAgentAddr(server.Listener.Addr().String()),
		WithProfileTypes(
			BlockProfile,
			CPUProfile,
			GoroutineProfile,
			HeapProfile,
			MetricsProfile,
			MutexProfile,
		),
		WithPeriod(period),
		CPUDuration(cpuDuration),
	)
	defer Stop()
	<-received
	profiles := <-received

	expected := []string{
		"data[cpu.pprof]",
		"data[delta-block.pprof]",
		"data[delta-heap.pprof]",
		"data[delta-mutex.pprof]",
		"data[goroutines.pprof]",
		"data[goroutineswait.pprof]",
	}
	if !testing.Short() {
		expected = append(expected, "data[metrics.json]")
	}
	sort.Strings(profiles)
	assert.Equal(t, expected, profiles)
}

func TestGoroutineWaitGoroutineLimit(t *testing.T) {
	exit := make(chan struct{})
	defer func() {
		close(exit)
	}()

	limit := 256
	testSetenv(t, "DD_PROFILING_WAIT_PROFILE_MAX_GOROUTINES", strconv.Itoa(limit))
	var ready sync.WaitGroup
	for i := 0; i < limit+1; i++ {
		ready.Add(1)
		go func() {
			ready.Done()
			<-exit
		}()
	}
	// Make sure all the goroutines have actually started before we continue
	ready.Wait()

	received := make(chan []string)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var profiles []string
		defer func() {
			select {
			case received <- profiles:
			default:
			}
		}()
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("bad media type: %s", err)
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				return
			}
			if err != nil {
				t.Fatalf("next part: %s", err)
			}
			if strings.Contains(p.FormName(), "pprof") && p.FileName() == "pprof-data" {
				profiles = append(profiles, p.FormName())
			}
		}
	}))
	defer server.Close()

	testSetenv(t, "DD_PROFILING_WAIT_PROFILE", "1")

	Start(
		WithAgentAddr(server.Listener.Addr().String()),
		WithPeriod(10*time.Millisecond),
		CPUDuration(time.Millisecond),
	)
	defer Stop()
	profiles := <-received

	assert.NotContains(t, profiles, "data[goroutineswait.pprof]")
}

func testSetenv(t *testing.T, key, value string) {
	// re-implemented testing.T.Setenv since that function requires Go 1.17
	old, ok := os.LookupEnv(key)
	os.Setenv(key, value)
	t.Cleanup(func() {
		if ok {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	})
}
