// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
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
		CPUDuration(time.Second),
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
	type profileMeta struct {
		tags  []string
		files []string
	}

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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var profile profileMeta
		defer func() {
			select {
			case received <- profile:
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
			if p.FormName() == "tags[]" {
				val, err := io.ReadAll(p)
				if err != nil {
					t.Fatalf("next part: %s", err)
				}
				profile.tags = append(profile.tags, string(val))
			}
			if p.FileName() == "pprof-data" {
				profile.files = append(profile.files, p.FormName())
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

	validateProfile := func(profile profileMeta, seq uint64) {
		expected := []string{
			"data[cpu.pprof]",
			"data[delta-block.pprof]",
			"data[delta-heap.pprof]",
			"data[delta-mutex.pprof]",
			"data[goroutines.pprof]",
			"data[goroutineswait.pprof]",
		}
		assert.ElementsMatch(t, expected, profile.files)

		assert.Contains(t, profile.tags, fmt.Sprintf("profile_seq:%d", seq))
	}

	validateProfile(<-received, 0)
	validateProfile(<-received, 1)
}

func TestCorrectTags(t *testing.T) {
	got := make(chan []string)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var tags []string
		defer func() {
			got <- tags
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
			if p.FormName() == "tags[]" {
				tag, err := io.ReadAll(p)
				if err != nil {
					t.Fatalf("reading tags: %s", err)
				}
				tags = append(tags, string(tag))
			}
		}
	}))
	defer server.Close()

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
		CPUDuration(10*time.Millisecond),
		WithService("xyz"),
		WithEnv("testing"),
		WithTags("foo:bar", "baz:bonk"),
	)
	defer Stop()
	expected := []string{
		"baz:bonk",
		"env:testing",
		"foo:bar",
		"service:xyz",
	}
	for i := 0; i < 20; i++ {
		// We check the tags we get several times to try to have a
		// better chance of catching a bug where the some of the tags
		// are clobbered due to a bug caused by the same
		// profiler-internal tag slice being appended to from different
		// goroutines concurrently.
		tags := <-got
		for _, tag := range expected {
			require.Contains(t, tags, tag)
		}
	}
}
