// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package phi_test

import (
	"net/http"

	"github.com/PhilipJovanovic/phi"
	phitrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/phi"
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
	router := phi.NewRouter()

	// Use the tracer middleware with the default service name "phi.router".
	router.Use(phitrace.Middleware())

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
	router := phi.NewRouter()

	// Use the tracer middleware with your desired service name.
	router.Use(phitrace.Middleware(phitrace.WithServiceName("chi-server")))

	// Set up some endpoints.
	router.Get("/", handler)

	// And start gathering request traces
	http.ListenAndServe(":8080", router)
}
