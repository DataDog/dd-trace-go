// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http_test

import (
	"net/http"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
)

func Example() {
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})
	http.ListenAndServe(":8080", mux)
}

func Example_withServiceName() {
	mux := httptrace.NewServeMux(httptrace.WithServiceName("my-service"))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})
	http.ListenAndServe(":8080", mux)
}

func ExampleTraceAndServe() {
	mux := http.NewServeMux()
	mux.Handle("/", traceMiddleware(mux, http.HandlerFunc(Index)))
	http.ListenAndServe(":8080", mux)
}

func Index(w http.ResponseWriter, _ *http.Request) {
	w.Write([]byte("Hello World!\n"))
}

// ExampleWrapClient provides an example of how to connect an incoming request span to an outgoing http call.
func ExampleWrapClient() {
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
