package namingschematest

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gintrace "github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var ginGonicGin = harness.TestCase{
	Name: instrumentation.PackageGinGonicGin,
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		// silence startup logs
		gin.SetMode(gin.ReleaseMode)

		mt := mocktracer.Start()
		defer mt.Stop()

		mux := gin.New()
		mux.Use(gintrace.Middleware(serviceOverride))
		mux.GET("/200", func(c *gin.Context) {
			c.Status(200)
		})
		r := httptest.NewRequest("GET", "/200", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)

		return mt.FinishedSpans()
	},
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"gin.router"},
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
