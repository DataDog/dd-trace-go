// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httprouter_test

import (
	"fmt"
	"log"
	"net/http"

	"github.com/julienschmidt/httprouter"

	httptrace "gopkg.in/CodapeWild/dd-trace-go.v1/contrib/julienschmidt/httprouter"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/tracer"
)

func Index(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	fmt.Fprint(w, "Welcome!\n")
}

func Hello(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	fmt.Fprintf(w, "hello, %s!\n", ps.ByName("name"))
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
