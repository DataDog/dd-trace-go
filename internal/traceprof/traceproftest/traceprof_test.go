// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

// traceproftest provides testing for cross-cutting tracer/profiler features.
// It's a separate package from traceprof to avoid circular dependencies.
package traceproftest

import (
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/traceprof"
	pb "gopkg.in/DataDog/dd-trace-go.v1/internal/traceprof/testapp"

	"github.com/stretchr/testify/require"
)

// TestEndpointsAndCodeHotspots tests the code hotspots and endpoint filtering
// feature implemented using pprof labels in the tracer which are picked up by
// the CPU profiler. This is done using a small test application that simulates
// a simple http or grpc workload.
func TestEndpointsAndCodeHotspots(t *testing.T) {
	// The amount of time the profiler should be able to detect. It's much lower
	// than CpuDuration to avoid flaky test behavior and because we're not trying
	// assert the quality of the profiler beyond the presence of the right
	// labels.
	const minCPUDuration = 10 * time.Millisecond

	// testCommon runs the common parts of this test.
	testCommon := func(t *testing.T, c AppConfig) (*pb.WorkRes, *CPUProfile) {
		// Simulate a cpu-heavy request with a short sql query. This is a best-case
		// scenario for CPU Code Hotspots.
		req := &pb.WorkReq{
			CpuDuration: int64(90 * time.Millisecond),
			SqlDuration: int64(10 * time.Millisecond),
		}

		// Rerun test a few times with doubled duration until it passes to avoid
		// flaky behavior in CI.
		var (
			prof *CPUProfile
			res  *pb.WorkRes
		)
		for attempt := 1; attempt <= 5; attempt++ {
			app := c.Start(t)
			defer app.Stop(t)

			res = app.WorkRequest(t, req)
			prof = app.CPUProfile(t)

			notEnoughSamples := (prof.Duration() < minCPUDuration) ||
				(prof.LabelsDuration(CustomLabels) < minCPUDuration) ||
				(c.CodeHotspots && prof.LabelsDuration(map[string]string{traceprof.LocalRootSpanID: res.LocalRootSpanId, traceprof.SpanID: res.SpanId}) < minCPUDuration) ||
				(c.Endpoints && prof.LabelDuration(traceprof.TraceEndpoint, c.AppType.Endpoint()) < minCPUDuration)
			if notEnoughSamples {
				req.CpuDuration *= 2
				req.SqlDuration *= 2
				t.Logf("attempt %d: not enough cpu samples, doubling duration", attempt)
				continue
			}
			require.True(t, ValidSpanID(res.SpanId))
			require.True(t, ValidSpanID(res.LocalRootSpanId))
			return res, prof
		}
		// Failed after 5 attempts, identify which condition wasn't met
		require.GreaterOrEqual(t, prof.Duration(), minCPUDuration)
		require.GreaterOrEqual(t, prof.LabelsDuration(CustomLabels), minCPUDuration)
		if c.Endpoints {
			require.GreaterOrEqual(t, prof.LabelDuration(traceprof.TraceEndpoint, c.AppType.Endpoint()), minCPUDuration)
		}
		if c.CodeHotspots {
			require.GreaterOrEqual(t, prof.LabelsDuration(map[string]string{traceprof.LocalRootSpanID: res.LocalRootSpanId, traceprof.SpanID: res.SpanId}), minCPUDuration)
		}
		return nil, nil
	}

	for _, appType := range []testAppType{Direct, HTTP, GRPC} {
		t.Run(string(appType), func(t *testing.T) {
			t.Run("none", func(t *testing.T) {
				res, prof := testCommon(t, AppConfig{
					AppType: appType,
				})
				require.Zero(t, prof.LabelDuration(traceprof.SpanID, res.SpanId))
				require.Zero(t, prof.LabelDuration(traceprof.LocalRootSpanID, res.LocalRootSpanId))
				require.Zero(t, prof.LabelDuration(traceprof.TraceEndpoint, appType.Endpoint()))
			})

			t.Run("endpoints", func(t *testing.T) {
				res, prof := testCommon(t, AppConfig{
					AppType:   appType,
					Endpoints: true,
				})
				require.Zero(t, prof.LabelDuration(traceprof.SpanID, res.SpanId))
				require.Zero(t, prof.LabelDuration(traceprof.LocalRootSpanID, res.LocalRootSpanId))
				require.GreaterOrEqual(t, prof.LabelDuration(traceprof.TraceEndpoint, appType.Endpoint()), minCPUDuration)
			})

			t.Run("code-hotspots", func(t *testing.T) {
				res, prof := testCommon(t, AppConfig{
					AppType:      appType,
					CodeHotspots: true,
				})
				require.GreaterOrEqual(t, prof.LabelsDuration(map[string]string{
					traceprof.SpanID:          res.SpanId,
					traceprof.LocalRootSpanID: res.LocalRootSpanId,
				}), minCPUDuration)
				require.Zero(t, prof.LabelDuration(traceprof.TraceEndpoint, appType.Endpoint()))
			})

			t.Run("all", func(t *testing.T) {
				res, prof := testCommon(t, AppConfig{
					AppType:      appType,
					CodeHotspots: true,
					Endpoints:    true,
				})
				wantLabels := map[string]string{
					traceprof.SpanID:          res.SpanId,
					traceprof.LocalRootSpanID: res.LocalRootSpanId,
				}
				wantLabels[traceprof.TraceEndpoint] = appType.Endpoint()
				require.GreaterOrEqual(t, prof.LabelsDuration(wantLabels), minCPUDuration)
			})

			t.Run("none-child-of", func(t *testing.T) {
				res, prof := testCommon(t, AppConfig{
					AppType: appType,
					ChildOf: true,
				})
				require.Zero(t, prof.LabelDuration(traceprof.SpanID, res.SpanId))
				require.Zero(t, prof.LabelDuration(traceprof.LocalRootSpanID, res.LocalRootSpanId))
				require.Zero(t, prof.LabelDuration(traceprof.TraceEndpoint, appType.Endpoint()))
			})

			t.Run("all-child-of", func(t *testing.T) {
				res, prof := testCommon(t, AppConfig{
					AppType:      appType,
					CodeHotspots: true,
					Endpoints:    true,
					ChildOf:      true,
				})
				wantLabels := map[string]string{
					traceprof.SpanID:          res.SpanId,
					traceprof.LocalRootSpanID: res.LocalRootSpanId,
				}
				if appType != Direct {
					wantLabels[traceprof.TraceEndpoint] = appType.Endpoint()
				}
				require.GreaterOrEqual(t, prof.LabelsDuration(wantLabels), minCPUDuration)
			})
		})
	}
}

// BenchmarkEndpointsAndHotspots tries to quantify the overhead (latency, pprof
// samples, cpu time, pprof size) of profiler endpoints and code hotspots. Use
// ./bench.sh for executing these benchmarks.
func BenchmarkEndpointsAndHotspots(b *testing.B) {
	benchCommon := func(b *testing.B, appType testAppType, req *pb.WorkReq) {
		config := AppConfig{
			CodeHotspots: os.Getenv("BENCH_ENDPOINTS") == "true",
			Endpoints:    os.Getenv("BENCH_HOTSPOTS") == "true",
			AppType:      appType,
		}
		app := config.Start(b)
		defer app.Stop(b)

		b.ResetTimer()
		var (
			wg           sync.WaitGroup
			concurrency  = runtime.GOMAXPROCS(0)
			startCPUTime = CPURusage(b)
		)
		for g := 0; g < concurrency; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < b.N; i++ {
					app.WorkRequest(b, req)
				}
			}()
		}
		wg.Wait()
		cpuTime := CPURusage(b) - startCPUTime
		b.StopTimer()

		prof := app.CPUProfile(b)
		expectedCPUTime := time.Duration(b.N) * time.Duration(req.CpuDuration)
		if expectedCPUTime >= 90*time.Millisecond {
			// sanity check profile results if enough samples can be expected
			require.Greater(b, prof.Duration(), time.Duration(0))
			if config.CodeHotspots {
				require.Greater(b, prof.LabelDuration(traceprof.SpanID, "*"), time.Duration(0))
				require.Greater(b, prof.LabelDuration(traceprof.LocalRootSpanID, "*"), time.Duration(0))
			}
			if config.Endpoints && appType != Direct {
				require.Greater(b, prof.LabelDuration(traceprof.TraceEndpoint, appType.Endpoint()), time.Duration(0))
			}
		}

		b.ReportMetric(float64(prof.Samples())/float64(b.N*concurrency), "pprof-samples/op")
		b.ReportMetric(float64(prof.Size())/float64(b.N*concurrency), "pprof-B/op")
		b.ReportMetric(float64(cpuTime)/float64(b.N*concurrency), "cpu-ns/op")
	}

	for _, appType := range []testAppType{Direct, HTTP, GRPC} {
		b.Run(string(appType), func(b *testing.B) {
			b.Run("hello-world", func(b *testing.B) {
				// Simulates a handler that does no actual work other than paying for
				// the instrumentation overhead.
				benchCommon(b, appType, &pb.WorkReq{
					CpuDuration: int64(0 * time.Millisecond),
					SqlDuration: int64(0 * time.Millisecond),
				})
			})

			b.Run("cpu-bound", func(b *testing.B) {
				benchCommon(b, appType, &pb.WorkReq{
					CpuDuration: int64(90 * time.Millisecond),
					SqlDuration: int64(10 * time.Millisecond),
				})
			})

			b.Run("io-bound", func(b *testing.B) {
				benchCommon(b, appType, &pb.WorkReq{
					CpuDuration: int64(10 * time.Millisecond),
					SqlDuration: int64(90 * time.Millisecond),
				})
			})
		})
	}
}

// Test that overriding the resource name for a span & endpoint is reflected in
// the profile.
func TestOverrideResourceName(t *testing.T) {
	tracer.Start(tracer.WithProfilerEndpoints(true))
	defer tracer.Stop()

	// Running for an arbitrary amount of time is a possible source of
	// flakiness, but the most we can do is keep trying longer and longer
	// until what we want shows up in the CPU profile.
	duration := 100 * time.Millisecond
	maxDuration := 5 * time.Second
	for duration <= maxDuration {
		cp := StartCPUProfile(t)
		span := tracer.StartSpan("testoverride", tracer.ResourceName("testoverride.old"))
		span.SetTag(ext.ResourceName, "testoverride.new")
		stop := make(chan struct{})
		go cpuHogUntil(stop)
		time.Sleep(duration)
		close(stop)
		span.Finish()

		prof := cp.Stop(t)
		if prof.LabelDuration(traceprof.TraceEndpoint, "testoverride.new") > 0 {
			break
		}
		duration *= 2
	}
}
