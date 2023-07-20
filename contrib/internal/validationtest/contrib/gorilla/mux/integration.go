// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mux

import (
	"net/http"
	"net/http/httptest"
	"testing"

	muxtrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/gorilla/mux"
)

type Integration struct {
	mux      *muxtrace.Router
	numSpans int
	opts     []muxtrace.RouterOption
}

func New() *Integration {
	return &Integration{
		opts: make([]muxtrace.RouterOption, 0),
	}
}

func (i *Integration) Name() string {
	return "gorilla/mux"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()

	i.mux = muxtrace.NewRouter(i.opts...)
	t.Cleanup(func() {
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()

	i.mux.Handle("/200", okHandler()).Host("localhost")
	r := httptest.NewRequest("GET", "http://localhost/200?token=value&id=3&name=5", nil)

	i.mux.ServeHTTP(httptest.NewRecorder(), r)
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, muxtrace.WithServiceName(name))
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("200!\n"))
	})
}
