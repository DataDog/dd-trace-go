// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package negroni

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/urfave/negroni"
	negronitrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/urfave/negroni"
)

type Integration struct {
	router   *negroni.Negroni
	numSpans int
	opts     []negronitrace.Option
}

func New() *Integration {
	return &Integration{
		opts: make([]negronitrace.Option, 0),
	}
}

func (i *Integration) Name() string {
	return "urfave/negroni"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("hi!"))
	})
	mux.HandleFunc("/err", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, fmt.Sprintf("%d!", 500), 500)
	})

	i.router = negroni.New()
	i.router.Use(negronitrace.Middleware(i.opts...))
	i.router.UseHandler(mux)

	t.Cleanup(func() {
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()

	r := httptest.NewRequest("GET", "/user", nil)
	w := httptest.NewRecorder()
	i.router.ServeHTTP(w, r)
	response := w.Result()
	defer response.Body.Close()
	assert.Equal(t, response.StatusCode, 200)
	i.numSpans++

	r = httptest.NewRequest("GET", "/err", nil)
	w = httptest.NewRecorder()
	i.router.ServeHTTP(w, r)
	response = w.Result()
	defer response.Body.Close()
	assert.Equal(t, response.StatusCode, 500)
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, negronitrace.WithServiceName(name))
}

type mockClient struct {
	code int
	err  error
}

func (mc *mockClient) Do(req *http.Request) (*http.Response, error) {
	if mc.err != nil {
		return nil, mc.err
	}
	// the request body in a response should be nil based on the documentation of http.Response
	req.Body = nil
	res := &http.Response{
		Status:     fmt.Sprintf("%d %s", mc.code, http.StatusText(mc.code)),
		StatusCode: mc.code,
		Proto:      req.Proto,
		ProtoMajor: req.ProtoMajor,
		ProtoMinor: req.ProtoMinor,
		Request:    req,
	}
	return res, nil
}
