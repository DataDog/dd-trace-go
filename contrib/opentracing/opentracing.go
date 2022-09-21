// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package opentracing

import (
	"net/http"

	"github.com/opentracing/opentracing-go"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/httpsec"
)

func AppSecMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := h
		defer func() {
			h.ServeHTTP(w, r)
		}()
		if !appsec.Enabled() {
			return
		}
		sp := opentracing.SpanFromContext(r.Context())
		if sp == nil {
			return
		}
		w = httptrace.WrapResponseWriter(w)
		h = httpsec.WrapHandler(h, span{Span: sp}, nil)
	})
}

type span struct {
	opentracing.Span
}

func (s span) SetTag(k string, v interface{}) {
	s.Span.SetTag(k, v)
}

func (s span) GetTag(k string, v interface{}) {
	s.Span.SetTag(k, v)
}
