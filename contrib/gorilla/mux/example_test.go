// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mux_test

import (
	"net/http"

	muxtrace "github.com/DataDog/dd-trace-go/contrib/gorilla/mux/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func handler(w http.ResponseWriter, _ *http.Request) {
	w.Write([]byte("Hello World!\n"))
}

func Example() {
	tracer.Start()
	defer tracer.Stop()

	mux := muxtrace.NewRouter()
	mux.HandleFunc("/", handler)
	http.ListenAndServe(":8080", mux)
}

func Example_withServiceName() {
	tracer.Start()
	defer tracer.Stop()

	mux := muxtrace.NewRouter(muxtrace.WithService("mux.route"))
	mux.HandleFunc("/", handler)
	http.ListenAndServe(":8080", mux)
}
