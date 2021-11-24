// traceproftest provides testing for cross-cutting tracer/profiler features.
// It's a separate package from traceprof to avoid circular dependencies.
package traceproftest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime/pprof"
	"strconv"
	"testing"
	"time"

	grpctrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/grpc"
	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/julienschmidt/httprouter"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/traceprof"
	pb "gopkg.in/DataDog/dd-trace-go.v1/internal/traceprof/testapp"

	pprofile "github.com/google/pprof/profile"
	"github.com/julienschmidt/httprouter"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

const (
	httpWorkEndpointMethod = "POST"
	httpWorkEndpoint       = "/work/:duration"
	grpcWorkEndpoint       = "/testapp.TestApp/Work"
)

var customLabels = map[string]string{"user label": "user val"}

// TestEndpointsAndCodeHotspots tests the code hotspots and endpoint filtering
// feature implemented using pprof labels in the tracer which are picked up by
// the CPU profiler. This is done using a small test application that simulates
// a simple http or grpc workload.
func TestEndpointsAndCodeHotspots(t *testing.T) {
	const (
		// cpuDuration is the amount of time the work handler of the test app
		// should spend on cpu work.
		cpuDuration = 100 * time.Millisecond
		// minCPUDuration is the amount of time the profiler should be able to
		// detect. It's much lower than cpuDuration to avoid flaky test behavior
		// and because we're not trying assert the quality of the profiler beyond
		// the presence of the right labels.
		minCPUDuration = cpuDuration / 10
	)

	assertCommon := func(t *testing.T, app *testApp, res *pb.WorkRes) {
		require.True(t, validSpanID(res.SpanId))
		require.True(t, validSpanID(res.LocalRootSpanId))
		require.Greater(t, app.CPUTime(t), minCPUDuration)
		require.Greater(t, app.LabelsCPUTime(t, customLabels), minCPUDuration)
	}

	for _, appType := range []testAppType{HTTP, GRPC} {
		var wantEndpoint string
		switch appType {
		case GRPC:
			wantEndpoint = grpcWorkEndpoint
		case HTTP:
			wantEndpoint = httpWorkEndpointMethod + " " + httpWorkEndpoint
		default:
			panic("unreachable")
		}

		t.Run(string(appType), func(t *testing.T) {
			t.Run("none", func(t *testing.T) {
				app := testAppConfig{AppType: appType}.Start(t)
				defer app.Stop(t)

				res := app.Request(t, cpuDuration)
				assertCommon(t, app, res)
				require.Zero(t, app.LabelCPUTime(t, traceprof.SpanID, res.SpanId))
				require.Zero(t, app.LabelCPUTime(t, traceprof.LocalRootSpanID, res.LocalRootSpanId))
				require.Zero(t, app.LabelCPUTime(t, traceprof.TraceEndpoint, wantEndpoint))
			})

			t.Run("endpoints", func(t *testing.T) {
				app := testAppConfig{AppType: appType, Endpoints: true}.Start(t)
				defer app.Stop(t)

				res := app.Request(t, cpuDuration)
				assertCommon(t, app, res)
				require.Zero(t, app.LabelCPUTime(t, traceprof.SpanID, res.SpanId))
				require.Zero(t, app.LabelCPUTime(t, traceprof.LocalRootSpanID, res.LocalRootSpanId))
				require.Greater(t, app.LabelCPUTime(t, traceprof.TraceEndpoint, wantEndpoint), minCPUDuration)
			})

			t.Run("code-hotspots", func(t *testing.T) {
				app := testAppConfig{AppType: appType, CodeHotspots: true}.Start(t)
				defer app.Stop(t)

				res := app.Request(t, cpuDuration)
				assertCommon(t, app, res)
				require.Greater(t, app.LabelsCPUTime(t, map[string]string{
					traceprof.SpanID:          res.SpanId,
					traceprof.LocalRootSpanID: res.LocalRootSpanId,
				}), minCPUDuration)
				require.Zero(t, app.LabelCPUTime(t, traceprof.TraceEndpoint, wantEndpoint))
			})

			t.Run("all", func(t *testing.T) {
				app := testAppConfig{AppType: appType, CodeHotspots: true, Endpoints: true}.Start(t)
				defer app.Stop(t)

				res := app.Request(t, cpuDuration)
				assertCommon(t, app, res)
				require.Greater(t, app.LabelsCPUTime(t, map[string]string{
					traceprof.SpanID:          res.SpanId,
					traceprof.LocalRootSpanID: res.LocalRootSpanId,
					traceprof.TraceEndpoint:   wantEndpoint,
				}), minCPUDuration)
			})

			t.Run("none-child-of", func(t *testing.T) {
				app := testAppConfig{AppType: appType, ChildOf: true}.Start(t)
				defer app.Stop(t)

				res := app.Request(t, cpuDuration)
				assertCommon(t, app, res)
				require.Zero(t, app.LabelCPUTime(t, traceprof.SpanID, res.SpanId))
				require.Zero(t, app.LabelCPUTime(t, traceprof.LocalRootSpanID, res.LocalRootSpanId))
				require.Zero(t, app.LabelCPUTime(t, traceprof.TraceEndpoint, wantEndpoint))
			})

			t.Run("all-child-of", func(t *testing.T) {
				app := testAppConfig{
					AppType:      appType,
					CodeHotspots: true,
					Endpoints:    true,
					ChildOf:      true,
				}.Start(t)
				defer app.Stop(t)

				res := app.Request(t, cpuDuration)
				assertCommon(t, app, res)
				require.Greater(t, app.LabelsCPUTime(t, map[string]string{
					traceprof.SpanID:          res.SpanId,
					traceprof.LocalRootSpanID: res.LocalRootSpanId,
					traceprof.TraceEndpoint:   wantEndpoint,
				}), minCPUDuration)
			})
		})
	}
}

// validSpanID returns true if id is a valid span id (random.Uint64()).
func validSpanID(id string) bool {
	val, err := strconv.ParseUint(id, 10, 64)
	return err == nil && val > 0
}

type testAppConfig struct {
	// Endpoints is passed to tracer.WithProfilerEndpoints()
	Endpoints bool
	// CodeHotspots is passed to tracer.WithProfilerCodeHotspots()
	CodeHotspots bool
	// ChildOf uses tracer.ChildOf() to declare the parent of cpuSpan instead of
	// tracer.StartSpanFromContext().
	ChildOf bool
	// AppType is the type of the test app that is being simulated.
	AppType testAppType
}

type testAppType string

const (
	GRPC testAppType = "grpc"
	HTTP testAppType = "http"
)

func (c testAppConfig) Start(t *testing.T) *testApp {
	a := &testApp{config: c}
	a.start(t)
	return a
}

type testApp struct {
	httpServer     *httptest.Server
	grpcServer     *grpc.Server
	grpcClientConn *grpc.ClientConn
	cpuBuf         bytes.Buffer
	cpuProf        *pprofile.Profile
	config         testAppConfig
	stopped        bool

	pb.UnimplementedTestAppServer
}

func (a *testApp) start(t *testing.T) {
	tracer.Start(
		tracer.WithLogger(log.DiscardLogger{}),
		tracer.WithProfilerCodeHotspots(a.config.CodeHotspots),
		tracer.WithProfilerEndpoints(a.config.Endpoints),
	)

	switch a.config.AppType {
	case GRPC:
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		si := grpctrace.StreamServerInterceptor(grpctrace.WithServiceName("my-grpc-client"))
		ui := grpctrace.UnaryServerInterceptor(grpctrace.WithServiceName("my-grpc-client"))
		a.grpcServer = grpc.NewServer(grpc.StreamInterceptor(si), grpc.UnaryInterceptor(ui))
		pb.RegisterTestAppServer(a.grpcServer, a)
		go a.grpcServer.Serve(l)
		a.grpcClientConn, err = grpc.Dial(l.Addr().String(), grpc.WithInsecure())
		require.NoError(t, err)
	case HTTP:
		router := httptrace.New()
		// We use a routing pattern here so our test can validate that potential
		// Personal Identifiable Information (PII) values, in this case :duration,
		// isn't beeing collected in the "trace endpoint" label.
		router.Handle("POST", httpWorkEndpoint, a.workHandler)
		a.httpServer = httptest.NewServer(router)
	default:
		panic("unreachable")
	}

	require.NoError(t, pprof.StartCPUProfile(&a.cpuBuf))
}

// Stop stops the app, tracer and cpu profiler in an idempotent fashion.
func (a *testApp) Stop(t *testing.T) {
	if a.stopped {
		return
	}
	pprof.StopCPUProfile()
	tracer.Stop()
	switch a.config.AppType {
	case GRPC:
		a.grpcServer.GracefulStop()
		a.grpcClientConn.Close()
	case HTTP:
		a.httpServer.Close()
	default:
		panic("unreachable")
	}
	var err error
	a.cpuProf, err = pprofile.ParseData(a.cpuBuf.Bytes())
	require.NoError(t, err)
	a.stopped = true
}

func (a *testApp) Request(t *testing.T, cpuTime time.Duration) *pb.WorkRes {
	switch a.config.AppType {
	case GRPC:
		client := pb.NewTestAppClient(a.grpcClientConn)
		req := &pb.WorkReq{CpuDuration: int64(cpuTime)}
		res, err := client.Work(context.Background(), req)
		require.NoError(t, err)
		return res
	case HTTP:
		url := a.httpServer.URL + "/work/" + cpuTime.String()
		res, err := http.Post(url, "text/plain", nil)
		require.NoError(t, err)

		defer res.Body.Close()
		workRes := &pb.WorkRes{}
		require.NoError(t, json.NewDecoder(res.Body).Decode(&workRes))
		return workRes
	default:
		panic("unreachable")
	}
}

// CPUTime stops the app and returns how much CPU time it spent according to
// the CPU profiler.
func (a *testApp) CPUTime(t *testing.T) (d time.Duration) {
	return a.LabelsCPUTime(t, nil)
}

// LabelCPUTime stops the app and returns how much CPU time it spent for the
// given pprof label according to the CPU profiler.
func (a *testApp) LabelCPUTime(t *testing.T, label, val string) (d time.Duration) {
	return a.LabelsCPUTime(t, map[string]string{label: val})
}

// LabelsCPUTime stops the app and returns how much CPU time it spent for the
// given pprof labels according to the CPU profiler.
func (a *testApp) LabelsCPUTime(t *testing.T, labels map[string]string) (d time.Duration) {
	a.Stop(t)
sampleloop:
	for _, s := range a.cpuProf.Sample {
		for k, v := range labels {
			if vals := s.Label[k]; len(vals) != 1 || vals[0] != v {
				continue sampleloop
			}
		}
		d += time.Duration(s.Value[1])
	}
	return d
}

func (a *testApp) workHandler(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	dt, err := time.ParseDuration(p.ByName("duration"))
	if err != nil {
		http.Error(w, "bad duration", http.StatusBadRequest)
		return
	}
	res, err := a.Work(r.Context(), &pb.WorkReq{CpuDuration: int64(dt)})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(res)
}

func (a *testApp) Work(ctx context.Context, req *pb.WorkReq) (*pb.WorkRes, error) {
	// Simulate user-defined custom labels to make sure we don't overwrite them
	// when we apply our own labels.
	ctx = pprof.WithLabels(ctx, toLabelSet(customLabels))
	pprof.SetGoroutineLabels(ctx)

	localRootSpan, _ := tracer.SpanFromContext(ctx)
	// We run our handler in a reqSpan so we can test that we still include the
	// correct "local root span id" in the profiler labels.
	reqSpan, reqSpanCtx := tracer.StartSpanFromContext(ctx, "workHandler")
	defer reqSpan.Finish()

	// fakeSQLQuery pretends to execute an APM instrumented SQL query. This tests
	// that the parent goroutine labels are correctly restored when it finishes.
	fakeSQLQuery(reqSpanCtx, "SELECT * FROM foo")

	var cpuSpan ddtrace.Span
	if a.config.ChildOf {
		cpuSpan = tracer.StartSpan("cpuHog", tracer.ChildOf(reqSpan.Context()))
	} else {
		cpuSpan, _ = tracer.StartSpanFromContext(reqSpanCtx, "cpuHog")
	}
	// Perform CPU intense work on another goroutine. This should still be
	// tracked to the childSpan thanks to goroutines inheriting labels.
	stop := make(chan struct{})
	go cpuHogUnil(stop)
	time.Sleep(time.Duration(req.CpuDuration))
	close(stop)
	cpuSpan.Finish()

	return &pb.WorkRes{
		LocalRootSpanId: fmt.Sprintf("%d", localRootSpan.Context().SpanID()),
		SpanId:          fmt.Sprintf("%d", cpuSpan.Context().SpanID()),
	}, nil
}

func toLabelSet(m map[string]string) pprof.LabelSet {
	var args []string
	for k, v := range m {
		args = append(args, k, v)
	}
	return pprof.Labels(args...)
}

func fakeSQLQuery(ctx context.Context, sql string) {
	span, _ := tracer.StartSpanFromContext(ctx, "pgx.query")
	defer span.Finish()
	span.SetTag(ext.ResourceName, sql)
	time.Sleep(10 * time.Millisecond)
}

func cpuHogUnil(stop chan struct{}) {
	for i := 0; ; i++ {
		select {
		case <-stop:
			return
		default:
			// burn cpu
			fmt.Fprintf(ioutil.Discard, "%d", i)
		}
	}
}
