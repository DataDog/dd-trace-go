// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http_test

import (
	"net/http"
	"net/http/httptest"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func Example() {
	tracer.Start()
	defer tracer.Stop()

	mux := httptrace.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})
	http.ListenAndServe(":8080", mux)
}

func Example_withServiceName() {
	tracer.Start()
	defer tracer.Stop()

	mux := httptrace.NewServeMux(httptrace.WithService("my-service"))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})
	http.ListenAndServe(":8080", mux)
}

func ExampleTraceAndServe() {
	tracer.Start()
	defer tracer.Stop()

	mux := http.NewServeMux()
	mux.Handle("/", traceMiddleware(mux, http.HandlerFunc(Index)))
	http.ListenAndServe(":8080", mux)
}

func Index(w http.ResponseWriter, _ *http.Request) {
	w.Write([]byte("Hello World!\n"))
}

// ExampleWrapClient provides an example of how to connect an incoming request span to an outgoing http call.
func ExampleWrapClient() {
	tracer.Start()
	defer tracer.Stop()

	mux := httptrace.NewServeMux()
	// Note that `WrapClient` modifies the passed in Client, so all other users of DefaultClient in this example will have a traced http Client
	c := httptrace.WrapClient(http.DefaultClient)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, "http://test.test", nil)
		resp, err := c.Do(req)
		if err != nil {
			w.Write([]byte(err.Error()))
			return
		}
		defer resp.Body.Close()
		w.Write([]byte(resp.Status))
	})
	http.ListenAndServe(":8080", mux)
}

// ExampleWrapClient_withClientTrace demonstrates how to enable detailed HTTP request tracing
// using httptrace.ClientTrace. This provides timing information for DNS lookups, connection
// establishment, TLS handshakes, and other HTTP request events as span tags.
func ExampleWrapClient_withClientTrace() {
	tracer.Start()
	defer tracer.Stop()

	// Create a test server for demonstration
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	defer server.Close()

	// Create an HTTP client with ClientTrace enabled
	c := httptrace.WrapClient(http.DefaultClient, httptrace.WithClientTrace(true))

	// Make a request - the span will include detailed timing information
	// such as http.dns.duration_ms, http.connect.duration_ms, etc.
	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	// The resulting span will contain timing tags like:
	// - http.dns.duration_ms: Time spent on DNS resolution
	// - http.connect.duration_ms: Time spent establishing connection
	// - http.tls.duration_ms: Time spent on TLS handshake
	// - http.get_conn.duration_ms: Time spent getting connection from pool
	// - http.first_byte.duration_ms: Time to first response byte
}

func traceMiddleware(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, route := mux.Handler(r)
		resource := r.Method + " " + route
		httptrace.TraceAndServe(next, w, r, &httptrace.ServeConfig{
			Service:     "http.router",
			Resource:    resource,
			QueryParams: true,
		})
	})
}
