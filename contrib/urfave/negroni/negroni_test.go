// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

// Package gorm provides helper functions for tracing the urfave/negroni package (https://github.com/urfave/negroni).
package negroni

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
	"github.com/urfave/negroni"
)

func TestChildSpan(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		_, ok := tracer.SpanFromContext(r.Context())
		assert.True(ok)
		w.WriteHeader(200)
	})

	router := negroni.New()
	router.Use(Middleware(WithServiceName("foobar")))

	router.UseHandler(mux)

	r := httptest.NewRequest("GET", "/user", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, r)
}

func TestTrace200(t *testing.T) {
	assertDoRequest := func(assert *assert.Assertions, mt mocktracer.Tracer, router *negroni.Negroni) {
		r := httptest.NewRequest("GET", "/user", nil)
		w := httptest.NewRecorder()

		// do and verify the request
		router.ServeHTTP(w, r)
		response := w.Result()
		assert.Equal(response.StatusCode, 200)

		// verify traces look good
		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		if len(spans) < 1 {
			t.Fatalf("no spans")
		}
		span := spans[0]
		assert.Equal("http.request", span.OperationName())
		assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
		assert.Equal("foobar", span.Tag(ext.ServiceName))
		assert.Equal("GET /user", span.Tag(ext.ResourceName))
		assert.Equal("200", span.Tag(ext.HTTPCode))
		assert.Equal("GET", span.Tag(ext.HTTPMethod))
		assert.Equal("/user", span.Tag(ext.HTTPURL))
	}

	t.Run("response written", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		mux := http.NewServeMux()
		mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
			span, ok := tracer.SpanFromContext(r.Context())
			assert.True(ok)
			assert.Equal(span.(mocktracer.Span).Tag(ext.ServiceName), "foobar")
			w.WriteHeader(200)
			w.Write([]byte("hi!"))
		})

		router := negroni.New()
		router.Use(Middleware(WithServiceName("foobar")))

		router.UseHandler(mux)

		assertDoRequest(assert, mt, router)
	})

	t.Run("no response written", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		mux := http.NewServeMux()
		mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
			span, ok := tracer.SpanFromContext(r.Context())
			assert.True(ok)
			assert.Equal(span.(mocktracer.Span).Tag(ext.ServiceName), "foobar")
			w.WriteHeader(200)
		})

		router := negroni.New()
		router.Use(Middleware(WithServiceName("foobar")))

		router.UseHandler(mux)

		assertDoRequest(assert, mt, router)
	})
}

func TestGetSpanNotInstrumented(t *testing.T) {
	assert := assert.New(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
	})

	router := negroni.New()
	router.Use(Middleware(WithServiceName("foobar")))

	router.UseHandler(mux)

	r := httptest.NewRequest("GET", "/user", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, r)
	response := w.Result()
	assert.Equal(response.StatusCode, 200)
}
