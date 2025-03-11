// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"net/http/httptest"
	"testing"

	"github.com/emicklei/go-restful/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	restfultrace "github.com/DataDog/dd-trace-go/contrib/emicklei/go-restful.v3/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var goRestfulV3 = harness.TestCase{
	Name: instrumentation.PackageEmickleiGoRestfulV3,
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []restfultrace.Option
		if serviceOverride != "" {
			opts = append(opts, restfultrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		ws := new(restful.WebService)
		ws.Filter(restfultrace.FilterFunc(opts...))
		ws.Route(ws.GET("/user/{id}").Param(restful.PathParameter("id", "user ID")).
			To(func(request *restful.Request, response *restful.Response) {
				_, err := response.Write([]byte(request.PathParameter("id")))
				require.NoError(t, err)
			}))
		container := restful.NewContainer()
		container.Add(ws)

		r := httptest.NewRequest("GET", "/user/200", nil)
		w := httptest.NewRecorder()
		container.ServeHTTP(w, r)

		return mt.FinishedSpans()
	},
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"go-restful"},
		DDService:       []string{"go-restful"},
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
