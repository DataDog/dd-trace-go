// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package kratos

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPFinalResponseStatus(t *testing.T) {
	for _, otel := range []bool{false, true} {
		t.Run("otel="+strconv.FormatBool(otel), func(t *testing.T) {
			t.Cleanup(func() { tracer.Start(tracer.WithAgentAddr("127.0.0.1:0")); tracer.Stop() })
			t.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", strconv.FormatBool(otel))
			tracer.Start(tracer.WithAgentAddr("127.0.0.1:0"))
			defer tracer.Stop()
			for _, code := range []int{200, 201, 204, 500} {
				for _, filtered := range []bool{false, true} {
					t.Run(strconv.Itoa(code)+"/filter="+strconv.FormatBool(filtered), func(t *testing.T) {
						mt := mocktracer.Start()
						defer mt.Stop()
						opts := []kratoshttp.ServerOption{kratoshttp.Middleware(Server())}
						if filtered {
							opts = append(opts, kratoshttp.Filter(HTTPServerFilter))
						}
						srv := kratoshttp.NewServer(opts...)
						srv.Route("/").GET("/status", func(ctx kratoshttp.Context) error {
							_, err := ctx.Middleware(func(context.Context, any) (any, error) { return nil, nil })(ctx, nil)
							if err != nil {
								return err
							}
							if filtered {
								assert.Empty(t, mt.FinishedSpans(), "response has not been written yet")
							}
							return ctx.String(code, "")
						})
						rw := httptest.NewRecorder()
						srv.ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/status", nil))
						require.Equal(t, code, rw.Code)
						span := findSpan(t, mt, "kratos.server.request", ext.SpanKindServer)
						tag := ext.HTTPCode
						if otel {
							tag = ext.HTTPResponseStatusCode
						}
						if filtered {
							assert.Equal(t, strconv.Itoa(code), span.Tag(tag))
							if code == 500 {
								assert.NotNil(t, span.Tag(ext.ErrorMsg))
							}
						} else {
							assert.Nil(t, span.Tag(tag), "unknown status must not be guessed")
						}
					})
				}
			}
		})
	}
}

func TestHTTPResponseFilterWriter(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write func(http.ResponseWriter)
		want  int
	}{
		{name: "implicit", write: func(http.ResponseWriter) {}, want: 200},
		{name: "body", write: func(w http.ResponseWriter) { _, _ = w.Write([]byte("ok")) }, want: 200},
		{name: "informational", write: func(w http.ResponseWriter) { w.WriteHeader(103); w.WriteHeader(201) }, want: 201},
		{name: "first_final_wins", write: func(w http.ResponseWriter) { w.WriteHeader(204); w.WriteHeader(500) }, want: 204},
		{name: "flush", write: func(w http.ResponseWriter) { w.(http.Flusher).Flush(); w.WriteHeader(204) }, want: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got int
			h := HTTPServerFilter(HTTPServerFilter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, flusher := w.(http.Flusher)
				require.True(t, flusher)
				_, hijacker := w.(http.Hijacker)
				require.False(t, hijacker)
				r.Context().Value(httpResponseKey{}).(*httpResponseState).register(func(code int) { got = code })
				tc.write(w)
			})))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestHTTPResponseFilterPanic(t *testing.T) {
	got := -1
	h := HTTPServerFilter(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		r.Context().Value(httpResponseKey{}).(*httpResponseState).register(func(code int) { got = code })
		panic("handler panic")
	}))
	require.Panics(t, func() { h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil)) })
	assert.Zero(t, got, "finish pending spans without fabricating a successful status")
}
