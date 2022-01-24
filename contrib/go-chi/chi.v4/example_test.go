// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi_test

import (
	"net/http"

	"github.com/go-chi/chi/v4"

	chitrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/go-chi/chi.v4"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func handler(w http.ResponseWriter, r *http.Request) {
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

func Example_withServiceName() {
	// Start the tracer
	tracer.Start()
	defer tracer.Stop()

	// Create a chi Router
	router := chi.NewRouter()

	// Use the tracer middleware with your desired service name.
	router.Use(chitrace.Middleware(chitrace.WithServiceName("chi-server")))

	// Set up some endpoints.
	router.Get("/", handler)

	// And start gathering request traces
	http.ListenAndServe(":8080", router)
}
