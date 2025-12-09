// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var (
	netHTTPServerServeMux = harness.TestCase{
		Name: instrumentation.PackageNetHTTP + "_server_serve_mux",
		GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
			var opts []httptrace.Option
			if serviceOverride != "" {
				opts = append(opts, httptrace.WithService(serviceOverride))
			}
			mt := mocktracer.Start()
			defer mt.Stop()

			mux := httptrace.NewServeMux(opts...)
			mux.HandleFunc("/200", func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("OK\n"))
			})
			r := httptest.NewRequest("GET", "http://localhost/200", nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)

			return mt.FinishedSpans()
		},
		WantServiceNameV0: harness.ServiceNameAssertions{
			Defaults:        []string{"http.router"},
			DDService:       []string{harness.TestDDService},
			ServiceOverride: []string{harness.TestServiceOverride},
		},
		AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 1)
			assert.Equal(t, "http.request", spans[0].OperationName())
		},
		AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 1)
			assert.Equal(t, "http.server.request", spans[0].OperationName())
		},
	}

	netHTTPServerWrapHandler = harness.TestCase{
		Name: instrumentation.PackageNetHTTP + "_server_wrap_handler",
		GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
			var opts []httptrace.Option
			if serviceOverride != "" {
				opts = append(opts, httptrace.WithService(serviceOverride))
			}
			mt := mocktracer.Start()
			defer mt.Stop()

			mux := http.NewServeMux()
			var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("OK\n"))
			})
			h = httptrace.WrapHandler(h, "", "", opts...)
			mux.Handle("/200", h)

			r := httptest.NewRequest("GET", "http://localhost/200", nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)

			return mt.FinishedSpans()
		},
		WantServiceNameV0: harness.ServiceNameAssertions{
			Defaults:        []string{"http.router"},
			DDService:       []string{harness.TestDDService},
			ServiceOverride: []string{harness.TestServiceOverride},
		},
		AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 1)
			assert.Equal(t, "http.request", spans[0].OperationName())
		},
		AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 1)
			assert.Equal(t, "http.server.request", spans[0].OperationName())
		},
	}

	netHTTPClient = harness.TestCase{
		Name: instrumentation.PackageNetHTTP + "_client",
		GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
			var opts []httptrace.RoundTripperOption
			if serviceOverride != "" {
				opts = append(opts, httptrace.WithService(serviceOverride))
			}
			mt := mocktracer.Start()
			defer mt.Stop()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("OK\n"))
			}))
			defer srv.Close()

			c := httptrace.WrapClient(&http.Client{}, opts...)
			req, err := http.NewRequest(http.MethodGet, srv.URL+"/200", nil)
			require.NoError(t, err)
			resp, err := c.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			return mt.FinishedSpans()
		},
		WantServiceNameV0: harness.ServiceNameAssertions{
			Defaults:        []string{""},
			DDService:       []string{""},
			ServiceOverride: []string{harness.TestServiceOverride},
		},
		AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 1)
			assert.Equal(t, "http.request", spans[0].OperationName())
		},
		AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 1)
			assert.Equal(t, "http.client.request", spans[0].OperationName())
		},
	}
)
