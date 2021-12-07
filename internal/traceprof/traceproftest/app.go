// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

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
	"testing"
	"time"

	grpctrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/grpc"
	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/julienschmidt/httprouter"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	pb "gopkg.in/DataDog/dd-trace-go.v1/internal/traceprof/testapp"

	"github.com/julienschmidt/httprouter"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

const (
	// HTTPWorkEndpointMethod is the http method used for the demo app.
	HTTPWorkEndpointMethod = "POST"
	// HTTPWorkEndpoint is the http endpoint used for the demo app.
	HTTPWorkEndpoint = "/work/:secret"
	// GRPCWorkEndpoint is the grpc endpoint used for the demo app.
	GRPCWorkEndpoint = "/testapp.TestApp/Work"
)

// CustomLabels are the user-defined pprof labels to apply in the work endpoint
// to simulate user label interacting with our own labels.
var CustomLabels = map[string]string{"user label": "user val"}

// AppConfig defines the behavior and profiling options for the demo app.
type AppConfig struct {
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
	// Direct directly executes requests logic without any transport overhead.
	Direct testAppType = "direct"
	// GRPC executes requests via GRPC.
	GRPC testAppType = "grpc"
	// HTTP executes requests via HTTP.
	HTTP testAppType = "http"
)

// Endpoint returns the "trace endpoint" label value for this app type.
func (a testAppType) Endpoint() string {
	switch a {
	case Direct:
		return ""
	case GRPC:
		return GRPCWorkEndpoint
	case HTTP:
		return HTTPWorkEndpointMethod + " " + HTTPWorkEndpoint
	default:
		panic("unreachable")
	}
}

// Start starts the demo app, including tracer and profiler.
func (c AppConfig) Start(t testing.TB) *App {
	a := &App{config: c}
	a.start(t)
	return a
}

// App is an instance of the demo app.
type App struct {
	httpServer     *httptest.Server
	grpcServer     *grpc.Server
	grpcClientConn *grpc.ClientConn
	CPUProfiler
	prof    *CPUProfile
	config  AppConfig
	stopped bool

	pb.UnimplementedTestAppServer
}

func (a *App) start(t testing.TB) {
	tracer.Start(
		tracer.WithLogger(log.DiscardLogger{}),
		tracer.WithProfilerCodeHotspots(a.config.CodeHotspots),
		tracer.WithProfilerEndpoints(a.config.Endpoints),
	)

	switch a.config.AppType {
	case Direct:
		// nothing to setup
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
		// Personal Identifiable Information (PII) values, in this case :secret,
		// isn't beeing collected in the "trace endpoint" label.
		router.Handle("POST", HTTPWorkEndpoint, a.workHandler)
		a.httpServer = httptest.NewServer(router)
	default:
		panic("unreachable")
	}
	a.CPUProfiler.start(t)
}

// Stop stops the app, tracer and cpu profiler in an idempotent fashion.
func (a *App) Stop(t testing.TB) {
	if a.stopped {
		return
	}
	a.prof = a.CPUProfiler.Stop(t)
	tracer.Stop()
	switch a.config.AppType {
	case Direct:
		// nothing to tear down
	case GRPC:
		a.grpcServer.GracefulStop()
		a.grpcClientConn.Close()
	case HTTP:
		a.httpServer.Close()
	default:
		panic("unreachable")
	}
	a.stopped = true
}

// WorkRequest sends the given req to the demo app and returns the response.
// The config.AppType determines how the request is made.
func (a *App) WorkRequest(t testing.TB, req *pb.WorkReq) *pb.WorkRes {
	switch a.config.AppType {
	case Direct:
		res, err := a.Work(context.Background(), req)
		require.NoError(t, err)
		return res
	case GRPC:
		client := pb.NewTestAppClient(a.grpcClientConn)
		res, err := client.Work(context.Background(), req)
		require.NoError(t, err)
		return res
	case HTTP:
		body, err := json.Marshal(req)
		require.NoError(t, err)
		url := a.httpServer.URL + "/work/secret-pii"
		res, err := http.Post(url, "text/plain", bytes.NewReader(body))
		require.NoError(t, err)

		defer res.Body.Close()
		workRes := &pb.WorkRes{}
		require.NoError(t, json.NewDecoder(res.Body).Decode(&workRes))
		return workRes
	default:
		panic("unreachable")
	}
}

// CPUProfile stops the app and returns its CPU profile.
func (a *App) CPUProfile(t testing.TB) *CPUProfile {
	a.Stop(t)
	return a.prof
}

func (a *App) workHandler(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	req := &pb.WorkReq{}
	if err := json.NewDecoder(r.Body).Decode(req); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
	}
	res, err := a.Work(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(res)
}

// Work implements the request handler for the demo app. It's reused regardless
// of the config.AppType.
func (a *App) Work(ctx context.Context, req *pb.WorkReq) (*pb.WorkRes, error) {
	// Simulate user-defined custom labels to make sure we don't overwrite them
	// when we apply our own labels.
	ctx = pprof.WithLabels(ctx, toLabelSet(CustomLabels))
	pprof.SetGoroutineLabels(ctx)

	localRootSpan, ok := tracer.SpanFromContext(ctx)
	// We run our handler in a reqSpan so we can test that we still include the
	// correct "local root span id" in the profiler labels.
	reqSpan, reqSpanCtx := tracer.StartSpanFromContext(ctx, "workHandler")
	defer reqSpan.Finish()
	if !ok {
		// when app type is Direct, reqSpan is our local root span
		localRootSpan = reqSpan
	}

	// fakeSQLQuery pretends to execute an APM instrumented SQL query. This tests
	// that the parent goroutine labels are correctly restored when it finishes.
	fakeSQLQuery(reqSpanCtx, "SELECT * FROM foo", time.Duration(req.SqlDuration))

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
	if req.CpuDuration > 0 {
		time.Sleep(time.Duration(req.CpuDuration))
	}
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

func fakeSQLQuery(ctx context.Context, sql string, d time.Duration) {
	span, _ := tracer.StartSpanFromContext(ctx, "pgx.query")
	defer span.Finish()
	span.SetTag(ext.ResourceName, sql)
	time.Sleep(d)
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
