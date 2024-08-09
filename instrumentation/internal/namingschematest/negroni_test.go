// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	negronitrace "github.com/DataDog/dd-trace-go/contrib/urfave/negroni/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/negroni"
)

var urfaveNegroni = testCase{
	name: instrumentation.PackageUrfaveNegroni,
	genSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []negronitrace.Option
		if serviceOverride != "" {
			opts = append(opts, negronitrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		mux := http.NewServeMux()
		mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
			_, err := w.Write([]byte("ok"))
			require.NoError(t, err)
		})
		router := negroni.New()
		router.Use(negronitrace.Middleware(opts...))
		router.UseHandler(mux)
		r := httptest.NewRequest("GET", "/200", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)

		return mt.FinishedSpans()
	},
	wantServiceNameV0: serviceNameAssertions{
		defaults:        []string{"negroni.router"},
		ddService:       []string{testDDService},
		serviceOverride: []string{testServiceOverride},
	},
	assertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "http.request", spans[0].OperationName())
	},
	assertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "http.server.request", spans[0].OperationName())
	},
}
