// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package http

import (
	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHttpTracer200(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	url := "/200"
	r := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	router().ServeHTTP(w, r)

	assert := assert.New(t)
	assert.Equal(200, w.Code)
	assert.Equal("OK\n", w.Body.String())

	spans := mt.FinishedSpans()
	assert.Equal(1, len(spans))

	s := spans[0]
	assert.Equal("http.request", s.OperationName())
	assert.Equal("my-service", s.Tag(ext.ServiceName))
	assert.Equal("GET "+url, s.Tag(ext.ResourceName))
	assert.Equal("200", s.Tag(ext.HTTPCode))
	assert.Equal("GET", s.Tag(ext.HTTPMethod))
	assert.Equal(url, s.Tag(ext.HTTPURL))
	assert.Equal(nil, s.Tag(ext.Error))
	assert.Equal("bar", s.Tag("foo"))
}

func TestHttpTracer500(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	// Send and verify a 500 request
	url := "/500"
	r := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	router().ServeHTTP(w, r)

	assert := assert.New(t)
	assert.Equal(500, w.Code)
	assert.Equal("500!\n", w.Body.String())

	spans := mt.FinishedSpans()
	assert.Equal(1, len(spans))

	s := spans[0]
	assert.Equal("http.request", s.OperationName())
	assert.Equal("my-service", s.Tag(ext.ServiceName))
	assert.Equal("GET "+url, s.Tag(ext.ResourceName))
	assert.Equal("500", s.Tag(ext.HTTPCode))
	assert.Equal("GET", s.Tag(ext.HTTPMethod))
	assert.Equal(url, s.Tag(ext.HTTPURL))
	assert.Equal("500: Internal Server Error", s.Tag(ext.Error).(error).Error())
	assert.Equal("bar", s.Tag("foo"))
}

func TestWrapHandler200(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	assert := assert.New(t)

	handler := WrapHandler(http.HandlerFunc(handler200), "my-service", "my-resource",
		WithSpanOptions(tracer.Tag("foo", "bar")),
	)

	url := "/"
	r := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	assert.Equal(200, w.Code)
	assert.Equal("OK\n", w.Body.String())

	spans := mt.FinishedSpans()
	assert.Equal(1, len(spans))

	s := spans[0]
	assert.Equal("http.request", s.OperationName())
	assert.Equal("my-service", s.Tag(ext.ServiceName))
	assert.Equal("my-resource", s.Tag(ext.ResourceName))
	assert.Equal("200", s.Tag(ext.HTTPCode))
	assert.Equal("GET", s.Tag(ext.HTTPMethod))
	assert.Equal(url, s.Tag(ext.HTTPURL))
	assert.Equal(nil, s.Tag(ext.Error))
	assert.Equal("bar", s.Tag("foo"))
}

func TestNoStack(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	assert := assert.New(t)

	handler := WrapHandler(http.HandlerFunc(handler500), "my-service", "my-resource",
		NoDebugStack())

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	assert.Equal(http.StatusInternalServerError, w.Code)
	assert.Equal("500!\n", w.Body.String())

	spans := mt.FinishedSpans()
	assert.Equal(1, len(spans))
	s := spans[0]
	assert.EqualError(spans[0].Tags()[ext.Error].(error), "500: Internal Server Error")
	assert.Equal("<mock no debug stack>", s.Tags()[ext.ErrorStack])

}

func TestAnalyticsSettings(t *testing.T) {
	tests := map[string]func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option){
		"ServeMux": func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
			mux := NewServeMux(opts...)
			mux.HandleFunc("/200", handler200)
			r := httptest.NewRequest("GET", "/200", nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)

			spans := mt.FinishedSpans()
			assert.Len(t, spans, 1)
			s := spans[0]
			assert.Equal(t, rate, s.Tag(ext.EventSampleRate))
		},
		"WrapHandler": func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
			f := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				message := "Hello \n"
				w.Write([]byte(message))
			})
			handler := WrapHandler(f, "my-service", "my-resource", opts...)
			r := httptest.NewRequest("GET", "/200", nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			spans := mt.FinishedSpans()
			assert.Len(t, spans, 1)
			s := spans[0]
			assert.Equal(t, rate, s.Tag(ext.EventSampleRate))
		},
	}

	for name, test := range tests {
		t.Run("defaults/"+name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			test(t, mt, nil)
		})

		t.Run("global/"+name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			rate := globalconfig.AnalyticsRate()
			defer globalconfig.SetAnalyticsRate(rate)
			globalconfig.SetAnalyticsRate(0.4)

			test(t, mt, 0.4)
		})

		t.Run("enabled/"+name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			test(t, mt, 1.0, WithAnalytics(true))
		})

		t.Run("disabled/"+name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			test(t, mt, nil, WithAnalytics(false))
		})

		t.Run("override/"+name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			rate := globalconfig.AnalyticsRate()
			defer globalconfig.SetAnalyticsRate(rate)
			globalconfig.SetAnalyticsRate(0.4)

			test(t, mt, 0.23, WithAnalyticsRate(0.23))
		})
	}
}

func router() http.Handler {
	mux := NewServeMux(WithServiceName("my-service"), WithSpanOptions(tracer.Tag("foo", "bar")))
	mux.HandleFunc("/200", handler200)
	mux.HandleFunc("/500", handler500)
	return mux
}

func handler200(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("OK\n"))
}

func handler500(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "500!", http.StatusInternalServerError)
}
