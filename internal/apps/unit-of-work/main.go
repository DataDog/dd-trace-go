// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/profiler"
)

var dummyData struct {
	marshal      map[string]int
	marshalCount int
}

func init() {
	dummyData.marshal = map[string]int{}
	for i := 0; i < 500; i++ {
		dummyData.marshal[fmt.Sprintf("key-%d", i)] = i
	}
}

func main() {
	// Enable unit of work
	os.Setenv("DD_PROFILING_ENDPOINT_COUNT_ENABLED", "true")

	// Parse flags
	var (
		httpF    = flag.String("http", "localhost:8080", "HTTP addr to listen on.")
		serviceF = flag.String("service", "dd-trace-go/unit-of-work", "Datadog service name")
		versionF = flag.String("version", "v1", "Datadog service version")
		periodF  = flag.Duration("period", 60*time.Second, "Profiling period.")
	)
	flag.IntVar(&dummyData.marshalCount, "marshalCount", 1000, "Number of times to call json.Marshal in FooHandler. BarHandler uses half of this value.")
	flag.Parse()

	// Setup context that gets canceled on receiving SIGINT
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Start tracer
	tracer.Start(
		tracer.WithService(*serviceF),
		tracer.WithServiceVersion(*versionF),
	)
	defer tracer.Stop()

	// Start profiler
	if err := profiler.Start(
		profiler.WithService(*serviceF),
		profiler.WithVersion(*versionF),
		profiler.WithPeriod(*periodF),
	); err != nil {
		log.Fatalf("failed to start profiler: %s", err)
	}
	defer profiler.Stop()

	// Setup http routes
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/foo", FooHandler)
	mux.HandleFunc("/bar", BarHandler)

	// Start http server
	l, err := net.Listen("tcp", *httpF)
	if err != nil {
		log.Fatalf("failed to listen: %s", err)
	}
	defer l.Close()
	log.Printf("Listening on: http://%s", *httpF)
	server := http.Server{Handler: mux}
	go server.Serve(l)

	// Wait until SIGINT is received, then shut down
	<-ctx.Done()
	log.Printf("Received interrupt, shutting down")
}

// FooHandler does twice the amount of dummy work per request as BarHandler.
func FooHandler(w http.ResponseWriter, _ *http.Request) {
	start := time.Now()
	for i := 0; i < dummyData.marshalCount; i++ {
		json.Marshal(dummyData.marshal)
	}
	fmt.Fprintf(w, "foo (%s)\n", time.Since(start))
}

// BarHandler does half the amount of dummy work per request as FooHandler.
func BarHandler(w http.ResponseWriter, _ *http.Request) {
	start := time.Now()
	for i := 0; i < dummyData.marshalCount/2; i++ {
		json.Marshal(dummyData.marshal)
	}
	fmt.Fprintf(w, "bar (%s)\n", time.Since(start))
}
