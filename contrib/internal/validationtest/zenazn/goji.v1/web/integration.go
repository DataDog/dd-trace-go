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
	"github.com/zenazn/goji/web"
	webtrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/zenazn/goji.v1/web"
)

type Integration struct {
	m        *web.Mux
	numSpans int
	opts     []webtrace.Option
}

func New() *Integration {
	return &Integration{
		opts: make([]webtrace.Option, 0),
	}
}

func (i *Integration) Name() string {
	return "zenazn/goji.v1/web"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()

	i.m = web.New()
	i.m.Use(webtrace.Middleware(i.opts...))
	i.m.Get("/user/:id", func(c web.C, w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})

	i.m.Get("/err", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, fmt.Sprintf("%d!", 500), 500)
	})

	t.Cleanup(func() {
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()
	i.m.ServeHTTP(w, r)

	response := w.Result()
	defer response.Body.Close()
	assert.Equal(t, response.StatusCode, 200)
	i.numSpans++

	r = httptest.NewRequest("GET", "err", nil)
	w = httptest.NewRecorder()
	i.m.ServeHTTP(w, r)

	response = w.Result()
	defer response.Body.Close()
	assert.Equal(t, response.StatusCode, 500)
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, webtrace.WithServiceName(name))
}
