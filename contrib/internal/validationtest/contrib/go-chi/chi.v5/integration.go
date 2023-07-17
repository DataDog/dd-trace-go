// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	chitrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/go-chi/chi.v5"
)

type Integration struct {
	router   *chi.Mux
	numSpans int
}

func New() *Integration {
	return &Integration{}
}

func (i *Integration) ResetNumSpans() {
	i.numSpans = 0
}

func (i *Integration) Name() string {
	return "contrib/go-chi/chi.v5"
}

func (i *Integration) Init(t *testing.T) func() {
	t.Helper()

	i.router = chi.NewRouter()
	i.router.Use(chitrace.Middleware())

	return func() {}
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()
	assert := assert.New(t)

	i.router.Get("/user/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, ok := tracer.SpanFromContext(r.Context())
		assert.True(ok)
	})
	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()

	i.router.ServeHTTP(w, r)
	i.numSpans++

	i.router.Get("/user2/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, ok := tracer.SpanFromContext(r.Context())
		assert.True(ok)
		id := chi.URLParam(r, "id")
		_, err := w.Write([]byte(id))
		assert.NoError(err)
	})

	r = httptest.NewRequest("GET", "/user2/123", nil)
	w = httptest.NewRecorder()

	// do and verify the request
	i.router.ServeHTTP(w, r)
	response := w.Result()
	defer response.Body.Close()
	assert.Equal(response.StatusCode, 200)
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}
