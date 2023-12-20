// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package apps

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/profiler"
)

// Config is the configuration for a test app used by RunHTTP.
type Config struct {
	// DisableExecutionTracing disables execution tracing for the test app. By
	// default we configure non-stop execution tracing for the test apps unless
	// a DD_PROFILING_EXECUTION_TRACE_PERIOD env is set or this option is true.
	DisableExecutionTracing bool
}

func (c Config) RunHTTP(handler func() http.Handler) {
	// Parse common test app flags
	var (
		httpF   = flag.String("http", "localhost:8080", "HTTP addr to listen on.")
		periodF = flag.Duration("period", 60*time.Second, "Profiling period.")
	)
	flag.Parse()

	// Configure non-stop execution tracing by default
	if v := os.Getenv("DD_PROFILING_EXECUTION_TRACE_PERIOD"); v == "" && !c.DisableExecutionTracing {
		os.Setenv("DD_PROFILING_EXECUTION_TRACE_PERIOD", "1s")
	}

	// Setup context that gets canceled on receiving SIGINT
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Start tracer
	tracer.Start(tracer.WithRuntimeMetrics())
	defer tracer.Stop()

	// Start the profiler
	if err := profiler.Start(
		profiler.WithPeriod(*periodF),
		profiler.WithProfileTypes(
			profiler.CPUProfile,
			profiler.HeapProfile,
			profiler.BlockProfile,
			profiler.MutexProfile,
			profiler.GoroutineProfile,
		),
	); err != nil {
		log.Fatalf("failed to start profiler: %s", err)
	}
	defer profiler.Stop()

	// Start http server
	l, err := net.Listen("tcp", *httpF)
	if err != nil {
		log.Fatalf("failed to listen: %s", err)
	}
	defer l.Close()
	log.Printf("Listening on: http://%s", *httpF)
	// handler is a func, because if we create a traced handler before starting
	// the tracer, the service name will default to http.router.
	server := http.Server{Handler: handler()}
	go server.Serve(l)

	// Wait until SIGINT is received, then shut down
	<-ctx.Done()
	log.Printf("Received interrupt, shutting down")
}
