// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
)

type Integration struct {
	router   http.Handler
	numSpans int
	opts     []httptrace.Option
}

func New() *Integration {
	return &Integration{
		opts: make([]httptrace.Option, 0),
	}
}

func (i *Integration) Name() string {
	return "net/http"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()

	i.router = router(i.opts...)

	t.Cleanup(func() {
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()

	url := "/200"
	r := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	i.router.ServeHTTP(w, r)
	i.numSpans++

	url = "/200/value"
	r = httptest.NewRequest("GET", url, nil)
	w = httptest.NewRecorder()
	i.router.ServeHTTP(w, r)
	i.numSpans++

	url = "/500"
	r = httptest.NewRequest("GET", url, nil)
	w = httptest.NewRecorder()
	i.router.ServeHTTP(w, r)
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, httptrace.WithServiceName(name))
}

func router(muxOpts ...httptrace.Option) http.Handler {
	mux := httptrace.NewServeMux(muxOpts...)
	mux.HandleFunc("/200", handler200)
	mux.HandleFunc("/500", handler500)
	return mux
}

func handler200(w http.ResponseWriter, _ *http.Request) {
	w.Write([]byte("OK\n"))
}

func handler500(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "500!", http.StatusInternalServerError)
}
