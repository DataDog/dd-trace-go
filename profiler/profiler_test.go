// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"runtime"
	"runtime/trace"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/httpmem"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/traceprof"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"

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
		assert.Contains(t, startupLog, "\"cpu\"")
		assert.Contains(t, startupLog, "\"heap\"")

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

	t.Run("aws-lambda", func(t *testing.T) {
		t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "my-function-name")
		err := Start()
		defer Stop()
		assert.NotNil(t, err)
	})
}

// TestStartWithoutStopReconfigures verifies that calling Start while the
// profiler is already running will restart it with the given configuration.
func TestStartWithoutStopReconfigures(t *testing.T) {
	got := make(chan profileMeta)
	server, client := httpmem.ServerAndClient(&mockBackend{t: t, profiles: got})
	defer server.Close()

	err := Start(
		WithHTTPClient(client),
		WithProfileTypes(HeapProfile),
		WithPeriod(200*time.Millisecond),
	)
	require.NoError(t, err)
	defer Stop()

	m := <-got
	if _, ok := m.attachments["delta-heap.pprof"]; !ok {
		t.Errorf("did not see a heap profile")
	}

	// Disable the heap profile, verify that we're not sending it
	err = Start(
		WithHTTPClient(client),
		WithProfileTypes(),
		WithPeriod(200*time.Millisecond),
	)
	require.NoError(t, err)

	m = <-got
	if _, ok := m.attachments["delta-heap.pprof"]; ok {
		t.Errorf("unexpectedly saw a heap profile")
	}
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
	if h := r.Header.Get("DD-Telemetry-Request-Type"); len(h) > 0 {
		return
	}
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

// startTestProfiler starts up a profiler wired up to an in-memory mock backend
// using the given profiler options, and returns a channel with the provided
// buffer size to which profiles will be sent. The profiler and mock backend
// will be stopped when the calling test case completes
func startTestProfiler(t *testing.T, size int, options ...Option) <-chan profileMeta {
	profiles := make(chan profileMeta, size)
	server, client := httpmem.ServerAndClient(&mockBackend{t: t, profiles: profiles})
	t.Cleanup(func() { server.Close() })

	options = append(options, WithHTTPClient(client))
	if err := Start(options...); err != nil {
		t.Fatalf("starting test profiler: %s", err)
	}
	t.Cleanup(Stop)
	return profiles
}

// doOneShortProfileUpload is a test helper which starts a profiler with a short
// period and no profile types enabled, intended to write quick tests which
// check that various metadata makes it through the profiler
func doOneShortProfileUpload(t *testing.T, opts ...Option) profileMeta {
	opts = append(opts,
		WithProfileTypes(), WithPeriod(10*time.Millisecond),
	)
	return <-startTestProfiler(t, 1, opts...)
}

func TestAllUploaded(t *testing.T) {
	// This is a kind of end-to-end test that runs the real profiles (i.e.
	// not mocking/replacing any internal functions) and verifies that the
	// profiles are at least uploaded.
	//
	// TODO: Further check that the uploaded profiles are all valid

	var customLabelKeys []string
	for i := 0; i < 50; i++ {
		customLabelKeys = append(customLabelKeys, strconv.Itoa(i))
	}

	t.Setenv("DD_PROFILING_WAIT_PROFILE", "1")
	t.Setenv("DD_PROFILING_EXECUTION_TRACE_PERIOD", "10ms") // match profile period
	// The channel is buffered with 2 entries so we can check that the
	// second batch of profiles is correct in case the profiler gets in a
	// bad state after the first round of profiling.
	profiles := startTestProfiler(t, 2,
		WithProfileTypes(
			BlockProfile,
			CPUProfile,
			GoroutineProfile,
			HeapProfile,
			MutexProfile,
		),
		WithPeriod(10*time.Millisecond),
		CPUDuration(1*time.Millisecond),
		WithCustomProfilerLabelKeys(customLabelKeys...),
	)

	validateProfile := func(profile profileMeta, seq uint64) {
		expected := []string{
			"cpu.pprof",
			"delta-block.pprof",
			"delta-heap.pprof",
			"delta-mutex.pprof",
			"goroutines.pprof",
			"goroutineswait.pprof",
		}
		if executionTraceEnabledDefault {
			expected = append(expected, "go.trace")
		}
		assert.ElementsMatch(t, expected, profile.event.Attachments)
		assert.ElementsMatch(t, customLabelKeys[:customProfileLabelLimit], profile.event.CustomAttributes)

		assert.Contains(t, profile.tags, fmt.Sprintf("profile_seq:%d", seq))

		assert.Equal(t, profile.event.Version, "4")
		assert.Equal(t, profile.event.Family, "go")
		assert.NotNil(t, profile.event.Start)
		assert.NotNil(t, profile.event.End)
	}

	validateProfile(<-profiles, 0)
	validateProfile(<-profiles, 1)
}

func TestCorrectTags(t *testing.T) {
	profiles := startTestProfiler(t, 1,
		WithProfileTypes(HeapProfile),
		WithPeriod(10*time.Millisecond),
		WithService("xyz"),
		WithEnv("testing"),
		WithTags("foo:bar", "baz:bonk"),
		WithHostname("example"),
	)
	expected := []string{
		"baz:bonk",
		"env:testing",
		"foo:bar",
		"service:xyz",
		"host:example",
		"runtime:go",
		fmt.Sprintf("process_id:%d", os.Getpid()),
		fmt.Sprintf("profiler_version:%s", version.Tag),
		fmt.Sprintf("runtime_version:%s", strings.TrimPrefix(runtime.Version(), "go")),
		fmt.Sprintf("runtime_compiler:%s", runtime.Compiler),
		fmt.Sprintf("runtime_arch:%s", runtime.GOARCH),
		fmt.Sprintf("runtime_os:%s", runtime.GOOS),
		fmt.Sprintf("runtime-id:%s", globalconfig.RuntimeID()),
	}
	for i := 0; i < 20; i++ {
		// We check the tags we get several times to try to have a
		// better chance of catching a bug where the some of the tags
		// are clobbered due to a bug caused by the same
		// profiler-internal tag slice being appended to from different
		// goroutines concurrently.
		p := <-profiles
		for _, tag := range expected {
			require.Contains(t, p.tags, tag)
		}
	}
}

func TestImmediateProfile(t *testing.T) {
	profiles := startTestProfiler(t, 1, WithProfileTypes(HeapProfile), WithPeriod(3*time.Second))

	// Wait a little less than 2 profile periods. We should start profiling
	// immediately. If it takes significantly longer than 1 profile period to get
	// a profile, consider the test failed
	timeout := time.After(5 * time.Second)
	select {
	case <-timeout:
		t.Fatal("should have received a profile already")
	case <-profiles:
	}
}

func TestExecutionTraceCPUProfileRate(t *testing.T) {
	// cpuProfileRate is picked randomly so we can check for it in the trace
	// data to reduce the chance that it occurs in the trace data for some other
	// reason. In theory we could use the entire int64 space, but when we do
	// this the runtime can crash with the error shown below.
	//
	// runtime: kevent on fd 3 failed with 60
	// fatal error: runtime: netpoll failed
	cpuProfileRate := int(9999 + rand.Int63n(9999))

	t.Setenv("DD_PROFILING_EXECUTION_TRACE_ENABLED", "true")
	t.Setenv("DD_PROFILING_EXECUTION_TRACE_PERIOD", "10ms")
	profile := <-startTestProfiler(t, 1,
		WithPeriod(10*time.Millisecond),
		WithProfileTypes(CPUProfile),
		CPUProfileRate(int(cpuProfileRate)),
	)
	assertContainsCPUProfileRateLog(t, profile.attachments["go.trace"], cpuProfileRate)
}

// assertContainsCPUProfileRateLog checks for the presence of the log written by
// traceLogCPUProfileRate. It's a bit hacky, but probably good enough for now :).
func assertContainsCPUProfileRateLog(t *testing.T, traceData []byte, cpuProfileRate int) {
	assert.True(t, bytes.Contains(traceData, []byte("cpuProfileRate")))
	assert.True(t, bytes.Contains(traceData, []byte(fmt.Sprintf("%d", cpuProfileRate))))
}

func sliceContains[T comparable](haystack []T, needle T) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func TestExecutionTraceMisconfiguration(t *testing.T) {
	rl := new(log.RecordLogger)
	defer log.UseLogger(rl)()
	// Test the profiler with an execution trace period of 0.
	// This is considered misconfiguration and tracing shouldn't be enabled.
	//
	// This test is partly defensive in nature: when doing randomized traces,
	// recording with probability (period / execution trace period),
	// depending on how it's implemented we may divide by 0. The Go
	// spec says that implementations _will_ panic for integer division by
	// 0, and _may_ choose to panic for floating point division by 0.
	// See go.dev/ref/spec#Arithmetic_operators, and go.dev/issue/43577
	t.Setenv("DD_PROFILING_EXECUTION_TRACE_ENABLED", "true")
	t.Setenv("DD_PROFILING_EXECUTION_TRACE_PERIOD", "0ms")
	profile := doOneShortProfileUpload(t,
		WithProfileTypes(),
		WithPeriod(10*time.Millisecond),
	)
	assert.NotContains(t, profile.event.Attachments, "go.trace")
	log.Flush()
	for _, m := range rl.Logs() {
		if strings.Contains(m, "Invalid execution trace config") {
			return
		}
	}
	t.Log(rl.Logs())
	t.Error("did not warn on invalid trace config")
}

func TestExecutionTraceRandom(t *testing.T) {
	collectTraces := func(t *testing.T, profilePeriod, tracePeriod time.Duration, count int) int {
		t.Setenv("DD_PROFILING_EXECUTION_TRACE_ENABLED", "true")
		t.Setenv("DD_PROFILING_EXECUTION_TRACE_PERIOD", tracePeriod.String())
		profiles := startTestProfiler(t, 10,
			WithProfileTypes(),
			WithPeriod(profilePeriod),
		)

		seenTraces := 0
		for i := 0; i < count; i++ {
			profile := <-profiles
			if sliceContains(profile.event.Attachments, "go.trace") && sliceContains(profile.tags, "go_execution_traced:yes") {
				seenTraces++
			} else if i == 0 {
				t.Error("did not see a trace in the first upload")
			}
		}
		return seenTraces
	}

	doTrial := func(t *testing.T, rate, tolerance float64) bool {
		profileDurationMs := 10.0
		traceDurationMs := profileDurationMs / rate
		const count = 100
		seen := collectTraces(t,
			time.Duration(profileDurationMs)*time.Millisecond,
			time.Duration(traceDurationMs)*time.Millisecond,
			count,
		)
		// We're simulating Bernoulli trials with the given rate, which
		// should follow a binomial distribution. Assert that we're
		// within the given number of standard deviations of the
		// expected mean
		stdDev := math.Sqrt(count * rate * (1 - rate))
		mean := count * rate
		lower, upper := int(mean-tolerance*stdDev), int(mean+tolerance*stdDev)
		if seen >= lower && seen <= upper {
			return true
		}
		t.Logf("observed %d traces, outside the desired bound of [%d, %d]", seen, lower, upper)
		return false
	}

	sampleRates := []float64{
		1.0 / 15.0, // our default rate
		0.5,        // higher to catch failures from under-sampling
		1.0,        // record all the time
	}
	for _, rate := range sampleRates {
		name := fmt.Sprintf("rate-%f", rate)
		t.Run(name, func(t *testing.T) {
			// We should be within 2 standard deviations ~95% of the time
			// with a correct implementation. If we do this four times, then
			// we have a ~99.999% chance of succeeding with a correct
			// implementation. We keep a reasonably tight tolerance
			// to ensure that an incorrect implementation is more likely
			// to fail each time
			for i := 0; i < 4; i++ {
				if doTrial(t, rate, 2.0) {
					return
				}
			}
			t.Error("failed after retry")
		})
	}
}

// TestEndpointCounts verfies that the unit of work feature works end to end.
func TestEndpointCounts(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		name := fmt.Sprintf("enabled=%v", enabled)
		t.Run(name, func(t *testing.T) {
			// Configure endpoint counting
			t.Setenv(traceprof.EndpointCountEnvVar, fmt.Sprintf("%v", enabled))

			// Start the tracer (before profiler to avoid race in case of slow tracer start)
			tracer.Start()
			defer tracer.Stop()

			profiles := startTestProfiler(t, 1,
				WithProfileTypes(CPUProfile),
				WithPeriod(100*time.Millisecond),
			)
			// Create spans until the first profile is finished
			var m profileMeta
			for m.attachments == nil {
				select {
				case m = <-profiles:
				default:
					span := tracer.StartSpan("http.request", tracer.ResourceName("/foo/bar"))
					span.Finish()
				}
			}

			// Check that the first uploaded profile matches our expectations
			if enabled {
				require.Equal(t, 1, len(m.event.EndpointCounts))
				require.Greater(t, m.event.EndpointCounts["/foo/bar"], uint64(0))
			} else {
				require.Empty(t, m.event.EndpointCounts)
			}
		})
	}
}

func TestExecutionTraceSizeLimit(t *testing.T) {
	done := make(chan struct{})
	// bigMessage just forces a bunch of data to be written to the trace buffer.
	// We'll write ~300k bytes per second, and try to stop at ~100k bytes with
	// a trace duration of 2 seconds, so we should stop early.
	bigMessage := string(make([]byte, 1024))
	tick := time.NewTicker(3 * time.Millisecond)
	defer tick.Stop()
	go func() {
		for {
			select {
			case <-tick.C:
				trace.Log(context.Background(), "msg", bigMessage)
			case <-done:
				return
			}
		}
	}()
	defer close(done)

	t.Setenv("DD_PROFILING_EXECUTION_TRACE_ENABLED", "true")
	t.Setenv("DD_PROFILING_EXECUTION_TRACE_PERIOD", "3s")
	t.Setenv("DD_PROFILING_EXECUTION_TRACE_LIMIT_BYTES", "100000")
	profiles := startTestProfiler(t, 1,
		WithProfileTypes(), // just want the execution trace
		WithPeriod(2*time.Second),
	)

	const expectedSize = 300 * 1024
	for i := 0; i < 5; i++ {
		m := <-profiles
		if p, ok := m.attachments["go.trace"]; ok {
			if len(p) > expectedSize {
				t.Fatalf("profile was too large: want %d, got %d", expectedSize, len(p))
			}
			return
		}
	}
}

func TestExecutionTraceEnabledFlag(t *testing.T) {
	for _, status := range []string{"true", "false"} {
		t.Run(status, func(t *testing.T) {
			t.Setenv("DD_PROFILING_EXECUTION_TRACE_ENABLED", status)
			t.Setenv("DD_PROFILING_EXECUTION_TRACE_PERIOD", "1s")
			profiles := startTestProfiler(t, 1,
				WithProfileTypes(),
				WithPeriod(10*time.Millisecond),
			)
			m := <-profiles
			t.Log(m.event.Attachments, m.tags)
			require.Contains(t, m.tags, fmt.Sprintf("_dd.profiler.go_execution_trace_enabled:%s", status))
		})
	}
}

func TestPgoTag(t *testing.T) {
	profiles := startTestProfiler(t, 1,
		WithProfileTypes(),
		WithPeriod(10*time.Millisecond),
	)
	m := <-profiles
	t.Log(m.event.Attachments, m.tags)
	require.Contains(t, m.tags, "pgo:false")
}

func TestVersionResolution(t *testing.T) {
	t.Run("tags only", func(t *testing.T) {
		data := doOneShortProfileUpload(t, WithTags("version:4.5.6", "version:7.8.9"))
		assert.Contains(t, data.tags, "version:4.5.6")
		assert.NotContains(t, data.tags, "version:7.8.9")
	})

	t.Run("env", func(t *testing.T) {
		// Environment variable gets priority over tags
		t.Setenv("DD_VERSION", "1.2.3")
		data := doOneShortProfileUpload(t, WithTags("version:4.5.6"))
		assert.NotContains(t, data.tags, "version:4.5.6")
		assert.Contains(t, data.tags, "version:1.2.3")
	})

	t.Run("WithVersion", func(t *testing.T) {
		// WithVersion gets the highest priority
		t.Setenv("DD_VERSION", "1.2.3")
		data := doOneShortProfileUpload(t, WithTags("version:4.5.6"), WithVersion("7.8.9"))
		assert.NotContains(t, data.tags, "version:1.2.3")
		assert.NotContains(t, data.tags, "version:4.5.6")
		assert.Contains(t, data.tags, "version:7.8.9")
	})

	t.Run("case insensitive", func(t *testing.T) {
		data := doOneShortProfileUpload(t, WithTags("Version:4.5.6", "version:7.8.9"))
		assert.Contains(t, data.tags, "Version:4.5.6")
		assert.NotContains(t, data.tags, "version:7.8.9")
	})
}

func TestUDSDefault(t *testing.T) {
	dir := t.TempDir()
	socket := path.Join(dir, "agent.socket")

	orig := internal.DefaultTraceAgentUDSPath
	defer func() {
		internal.DefaultTraceAgentUDSPath = orig
	}()
	internal.DefaultTraceAgentUDSPath = socket

	profiles := make(chan profileMeta, 1)
	backend := &mockBackend{t: t, profiles: profiles}
	mux := http.NewServeMux()
	// Specifically set up a handler for /profiling/v1/input to test that we
	// don't use the filesystem path to the Unix domain socket in the HTTP
	// request path.
	mux.Handle("/profiling/v1/input", backend)
	server := httptest.NewUnstartedServer(mux)
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	server.Listener = l
	server.Start()
	defer server.Close()

	err = Start(WithProfileTypes(), WithPeriod(10*time.Millisecond))
	require.NoError(t, err)
	defer Stop()

	<-profiles
}
