// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	chitrace "github.com/DataDog/dd-trace-go/contrib/go-chi/chi/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/go-chi/chi"
)

func handler(w http.ResponseWriter, _ *http.Request) {
	w.Write([]byte("Hello World!\n"))
}

func Example() {
	// Start the tracer
	tracer.Start()
	defer tracer.Stop()

	// Create a chi Router
	router := chi.NewRouter()

	// Use the tracer middleware with the default service name "chi.router".
	router.Use(chitrace.Middleware())

	// Set up some endpoints.
	router.Get("/", handler)

	// And start gathering request traces
	http.ListenAndServe(":8080", router)
}

func Example_orchestrion() {
	// Orchestrion or otelc adds tracing middleware and an endpoint hook.
	r := chi.NewRouter()
	r.Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "user %s", chi.URLParam(r, "id"))
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/users/42", nil))
	fmt.Println(w.Body.String())
	// Output:
	// user 42
}

func Example_pathParameters() {
	r := chi.NewRouter()
	// The root middleware covers unmatched requests and adds one span.
	r.Use(chitrace.Middleware())
	// The endpoint middleware gives AppSec the matched parameters.
	r.With(chitrace.Middleware()).Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "user %s", chi.URLParam(r, "id"))
	})
	r.Group(func(g chi.Router) {
		g.Use(chitrace.Middleware())
		g.Get("/orders/{id}", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "order %s", chi.URLParam(r, "id"))
		})
	})
	for _, path := range []string{"/users/42", "/orders/7"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		fmt.Println(w.Body.String())
	}
	// Output:
	// user 42
	// order 7
}

func Example_withServiceName() {
	// Start the tracer
	tracer.Start()
	defer tracer.Stop()

	// Create a chi Router
	router := chi.NewRouter()

	// Use the tracer middleware with your desired service name.
	router.Use(chitrace.Middleware(chitrace.WithService("chi-server")))

	// Set up some endpoints.
	router.Get("/", handler)

	// And start gathering request traces
	http.ListenAndServe(":8080", router)
}
