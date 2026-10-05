// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http_test

import (
	"log"
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

// Example_withServeMux shows how to make the traced ServeMux the outermost handler in a
// chain of wrapped handlers, by having it wrap a *http.ServeMux that was already configured
// elsewhere (here, with its own logging middleware). Since the traced mux runs first, it has
// already added trace context to the request by the time logMiddleware runs, so the logger
// can include dd.trace_id and dd.span_id.
func Example_withServeMux() {
	tracer.Start()
	defer tracer.Stop()

	appMux := http.NewServeMux()
	appMux.Handle("/", logMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})))

	mux := httptrace.NewServeMux(httptrace.WithServeMux(appMux))
	http.ListenAndServe(":8080", mux)
}

func logMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if span, ok := tracer.SpanFromContext(r.Context()); ok {
			log.Printf("dd.trace_id=%s dd.span_id=%d", span.Context().TraceID(), span.Context().SpanID())
		}
		h.ServeHTTP(w, r)
	})
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

// ExampleWrapClient_withClientTimings demonstrates how to enable detailed HTTP request tracing
// using httptrace.ClientTrace. This provides timing information for DNS lookups, connection
// establishment, TLS handshakes, and other HTTP request events as span tags.
func ExampleWrapClient_withClientTimings() {
	tracer.Start()
	defer tracer.Stop()

	// Create a test server for demonstration
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	defer server.Close()

	// Create an HTTP client with ClientTimings enabled
	c := httptrace.WrapClient(http.DefaultClient, httptrace.WithClientTimings(true))

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
