// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httprouter

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/julienschmidt/httprouter"
	httproutertrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/julienschmidt/httprouter"
)

type Integration struct {
	router   http.Handler
	numSpans int
	opts     []httproutertrace.RouterOption
}

func New() *Integration {
	return &Integration{
		opts: make([]httproutertrace.RouterOption, 0),
	}
}

func (i *Integration) Name() string {
	return "julienschmidt/httprouter"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()

	i.router = router(i.opts)

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
	i.opts = append(i.opts, httproutertrace.WithServiceName(name))
}

func router(opts []httproutertrace.RouterOption) http.Handler {
	router := httproutertrace.New(opts...)

	router.GET("/200", handler200)
	router.GET("/200/:parameter", handler200Parameter)
	router.GET("/500", handler500)

	return router
}

func handler200(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	w.Write([]byte("OK\n"))
}

func handler200Parameter(w http.ResponseWriter, _ *http.Request, p httprouter.Params) {
	w.Write([]byte(p.ByName("parameter")))
}

func handler500(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	http.Error(w, "500!", http.StatusInternalServerError)
}
