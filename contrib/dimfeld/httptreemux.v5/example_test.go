// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptreemux_test

import (
	"fmt"
	"log"
	"net/http"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/dimfeld/httptreemux.v5"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func Index(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
	fmt.Fprint(w, "Welcome!\n")
}

func Hello(w http.ResponseWriter, _ *http.Request, params map[string]string) {
	fmt.Fprintf(w, "hello, %s!\n", params["name"])
}

func Example() {
	router := httptrace.New()
	router.GET("/", Index)
	router.GET("/hello/:name", Hello)

	log.Fatal(http.ListenAndServe(":8080", router))
}

func Example_withServiceName() {
	router := httptrace.New(httptrace.WithServiceName("http.router"))
	router.GET("/", Index)
	router.GET("/hello/:name", Hello)

	log.Fatal(http.ListenAndServe(":8080", router))
}

func Example_withSpanOpts() {
	router := httptrace.New(
		httptrace.WithServiceName("http.router"),
		httptrace.WithSpanOptions(
			tracer.Tag(ext.SamplingPriority, ext.PriorityUserKeep),
		),
	)

	router.GET("/", Index)
	router.GET("/hello/:name", Hello)

	log.Fatal(http.ListenAndServe(":8080", router))
}
