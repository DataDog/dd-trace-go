// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

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
	t.Skip("broken test, see issue #1294")
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
	timeout := 200 * time.Millisecond
	select {
	case <-stopped:
	case <-time.After(timeout):
		// Capture stacks so we can see which goroutines are hanging and why.
		stacks := make([]byte, 64*1024)
		stacks = stacks[0:runtime.Stack(stacks, true)]
		t.Fatalf("Stop() took longer than %s:\n%s", timeout, stacks)
	}
}

func TestProfilerInternal(t *testing.T) {
	t.Run("collect", func(t *testing.T) {
		p, err := unstartedProfiler(
			WithPeriod(1*time.Millisecond),
			CPUDuration(1*time.Millisecond),
			WithProfileTypes(HeapProfile, CPUProfile),
		)
		require.NoError(t, err)
		var startCPU, stopCPU, writeHeap uint64
		p.testHooks.startCPUProfile = func(_ io.Writer) error {
			atomic.AddUint64(&startCPU, 1)
			return nil
		}
		p.testHooks.stopCPUProfile = func() { atomic.AddUint64(&stopCPU, 1) }
		p.testHooks.lookupProfile = func(name string, w io.Writer, _ int) error {
			if name == "heap" {
				atomic.AddUint64(&writeHeap, 1)
			}
			_, err := w.Write(textProfile{Text: "main 5\n"}.Protobuf())
			return err
		}

		tick := make(chan time.Time)
		wait := make(chan struct{})

		go func() {
			p.collect(tick)
			close(wait)
		}()

		tick <- time.Now()

		var bat batch
		select {
		case bat = <-p.out:
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("missing batch")
		}

		assert := assert.New(t)
		assert.EqualValues(1, writeHeap)
		assert.EqualValues(1, startCPU)
		assert.EqualValues(1, stopCPU)

		// should contain cpu.pprof, metrics.json, delta-heap.pprof
		assert.Equal(3, len(bat.profiles))

		p.exit <- struct{}{}
		<-wait
	})
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

func TestAllUploaded(t *testing.T) {
	// This is a kind of end-to-end test that runs the real profiles (i.e.
	// not mocking/replacing any internal functions) and verifies that the
	// profiles are at least uploaded.
	//
	// TODO: Further check that the uploaded profiles are all valid
	var (
		profiles []string
	)
	// received indicates that the server has received a profile upload.
	// This is used similarly to a sync.WaitGroup but avoids a potential
	// panic if too many requests are received before profiling is stopped
	// and the WaitGroup count goes negative.
	//
	// The channel is buffered with 2 entries so we can check that the
	// second batch of profiles is correct in case the profiler gets in a
	// bad state after the first round of profiling.
	received := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			select {
			case received <- struct{}{}:
			default:
			}
		}()
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("bad media type: %s", err)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		profiles = profiles[:0]
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				return
			}
			if err != nil {
				t.Fatalf("next part: %s", err)
			}
			if p.FileName() == "pprof-data" {
				profiles = append(profiles, p.FormName())
			}
		}
	}))
	defer server.Close()

	// re-implemented testing.T.Setenv since that function requires Go 1.17
	old, ok := os.LookupEnv("DD_PROFILING_WAIT_PROFILE")
	os.Setenv("DD_PROFILING_WAIT_PROFILE", "1")
	if ok {
		defer os.Setenv("DD_PROFILING_WAIT_PROFILE", old)
	} else {
		defer os.Unsetenv("DD_PROFILING_WAIT_PROFILE")
	}
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
	<-received
	<-received

	expected := []string{
		"data[cpu.pprof]",
		"data[delta-block.pprof]",
		"data[delta-heap.pprof]",
		"data[delta-mutex.pprof]",
		"data[goroutines.pprof]",
		"data[goroutineswait.pprof]",
	}
	sort.Strings(profiles)
	assert.Equal(t, expected, profiles)
}
