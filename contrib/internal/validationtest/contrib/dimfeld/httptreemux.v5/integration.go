// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptreemuxv5

import (
	"fmt"
	"log"
	"net/http"
	"testing"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/dimfeld/httptreemux.v5"
)

func Index(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
	fmt.Fprint(w, "Welcome!\n")
}

func Hello(w http.ResponseWriter, _ *http.Request, params map[string]string) {
	fmt.Fprintf(w, "hello, %s!\n", params["name"])
}

type Integration struct {
	router   *httptrace.Router
	numSpans int
}

func New() *Integration {
	return &Integration{}
}

func (i *Integration) ResetNumSpans() {
	i.numSpans = 0
}

func (i *Integration) Name() string {
	return "contrib/dimfeld/httptreemux.v5"
}

func (i *Integration) Init(t *testing.T) func() {
	t.Helper()
	i.router = httptrace.New()
	return func() {}
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()

	i.router.GET("/", Index)
	i.numSpans++

	i.router.GET("/hello/:name", Hello)
	i.numSpans++

	log.Fatal(http.ListenAndServe(":8080", i.router))
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}
