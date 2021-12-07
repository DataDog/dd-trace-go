// traceproftest provides testing for cross-cutting tracer/profiler features.
// It's a separate package from traceprof to avoid circular dependencies.
package traceproftest

import (
	"os"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/traceprof"
	pb "gopkg.in/DataDog/dd-trace-go.v1/internal/traceprof/testapp"

	"github.com/stretchr/testify/require"
)

// TestEndpointsAndCodeHotspots tests the code hotspots and endpoint filtering
// feature implemented using pprof labels in the tracer which are picked up by
// the CPU profiler. This is done using a small test application that simulates
// a simple http or grpc workload.
func TestEndpointsAndCodeHotspots(t *testing.T) {
	// Simulate a cpu-heavy request with a short sql query. This is a best-case
	// scenario for CPU Code Hotspots.
	req := &pb.WorkReq{
		CpuDuration: int64(90 * time.Millisecond),
		SqlDuration: int64(10 * time.Millisecond),
	}

	// The amount of time the profiler should be able to detect. It's much lower
	// than CpuDuration to avoid flaky test behavior and because we're not trying
	// assert the quality of the profiler beyond the presence of the right
	// labels.
	const minCPUDuration = 10 * time.Millisecond

	// assertCommon asserts invariants that apply to all tests
	assertCommon := func(t *testing.T, app *App, res *pb.WorkRes) {
		prof := app.CPUProfile(t)
		require.True(t, ValidSpanID(res.SpanId))
		require.True(t, ValidSpanID(res.LocalRootSpanId))
		require.GreaterOrEqual(t, prof.Duration(), minCPUDuration)
		require.GreaterOrEqual(t, prof.LabelsDuration(CustomLabels), minCPUDuration)
	}

	for _, appType := range []testAppType{Direct, HTTP, GRPC} {
		t.Run(string(appType), func(t *testing.T) {
			t.Run("none", func(t *testing.T) {
				app := AppConfig{AppType: appType}.Start(t)
				defer app.Stop(t)

				res := app.WorkRequest(t, req)
				assertCommon(t, app, res)
				prof := app.CPUProfile(t)
				require.Zero(t, prof.LabelDuration(traceprof.SpanID, res.SpanId))
				require.Zero(t, prof.LabelDuration(traceprof.LocalRootSpanID, res.LocalRootSpanId))
				require.Zero(t, prof.LabelDuration(traceprof.TraceEndpoint, appType.Endpoint()))
			})

			t.Run("endpoints", func(t *testing.T) {
				app := AppConfig{AppType: appType, Endpoints: true}.Start(t)
				defer app.Stop(t)

				res := app.WorkRequest(t, req)
				assertCommon(t, app, res)
				prof := app.CPUProfile(t)
				require.Zero(t, prof.LabelDuration(traceprof.SpanID, res.SpanId))
				require.Zero(t, prof.LabelDuration(traceprof.LocalRootSpanID, res.LocalRootSpanId))
				if appType != Direct {
					require.GreaterOrEqual(t, prof.LabelDuration(traceprof.TraceEndpoint, appType.Endpoint()), minCPUDuration)
				}
			})

			t.Run("code-hotspots", func(t *testing.T) {
				app := AppConfig{AppType: appType, CodeHotspots: true}.Start(t)
				defer app.Stop(t)

				res := app.WorkRequest(t, req)
				assertCommon(t, app, res)
				prof := app.CPUProfile(t)
				require.GreaterOrEqual(t, prof.LabelsDuration(map[string]string{
					traceprof.SpanID:          res.SpanId,
					traceprof.LocalRootSpanID: res.LocalRootSpanId,
				}), minCPUDuration)
				require.Zero(t, prof.LabelDuration(traceprof.TraceEndpoint, appType.Endpoint()))
			})

			t.Run("all", func(t *testing.T) {
				app := AppConfig{AppType: appType, CodeHotspots: true, Endpoints: true}.Start(t)
				defer app.Stop(t)

				res := app.WorkRequest(t, req)
				assertCommon(t, app, res)
				prof := app.CPUProfile(t)
				wantLabels := map[string]string{
					traceprof.SpanID:          res.SpanId,
					traceprof.LocalRootSpanID: res.LocalRootSpanId,
				}
				if appType != Direct {
					wantLabels[traceprof.TraceEndpoint] = appType.Endpoint()
				}
				require.GreaterOrEqual(t, prof.LabelsDuration(wantLabels), minCPUDuration)
			})

			t.Run("none-child-of", func(t *testing.T) {
				app := AppConfig{AppType: appType, ChildOf: true}.Start(t)
				defer app.Stop(t)

				res := app.WorkRequest(t, req)
				assertCommon(t, app, res)
				prof := app.CPUProfile(t)
				require.Zero(t, prof.LabelDuration(traceprof.SpanID, res.SpanId))
				require.Zero(t, prof.LabelDuration(traceprof.LocalRootSpanID, res.LocalRootSpanId))
				require.Zero(t, prof.LabelDuration(traceprof.TraceEndpoint, appType.Endpoint()))
			})

			t.Run("all-child-of", func(t *testing.T) {
				app := AppConfig{
					AppType:      appType,
					CodeHotspots: true,
					Endpoints:    true,
					ChildOf:      true,
				}.Start(t)
				defer app.Stop(t)

				res := app.WorkRequest(t, req)
				assertCommon(t, app, res)
				prof := app.CPUProfile(t)
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
		for i := 0; i < b.N; i++ {
			app.WorkRequest(b, req)
		}
		b.StopTimer()

		prof := app.CPUProfile(b)
		cpuTime := time.Duration(b.N) * time.Duration(req.CpuDuration)
		if cpuTime >= 90*time.Millisecond {
			// sanity check profile results if enough samples can be expected
			require.Greater(b, prof.Duration(), time.Duration(0))
			require.Greater(b, prof.LabelDuration("span id", "*"), time.Duration(0))
			require.Greater(b, prof.LabelDuration("local root span id", "*"), time.Duration(0))
			if config.Endpoints && appType != Direct {
				require.Greater(b, prof.LabelDuration("trace endpoint", appType.Endpoint()), time.Duration(0))
			}
		}

		b.ReportMetric(float64(prof.Samples())/float64(b.N), "pprof-samples/op")
		b.ReportMetric(float64(prof.Size())/float64(b.N), "pprof-B/op")
		b.ReportMetric(float64(prof.Duration())/float64(b.N), "cpu-ns/op")
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
