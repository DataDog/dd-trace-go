// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/julienschmidt/httprouter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	httproutertrace "github.com/DataDog/dd-trace-go/contrib/julienschmidt/httprouter/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var julienschmidtHTTPRouter = harness.TestCase{
	Name: instrumentation.PackageNetHTTP + "_server",
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []httproutertrace.RouterOption
		if serviceOverride != "" {
			opts = append(opts, httproutertrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		mux := httproutertrace.New(opts...)
		mux.GET("/200", func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
			w.Write([]byte("OK\n"))
		})
		r := httptest.NewRequest("GET", "/200", nil)
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
