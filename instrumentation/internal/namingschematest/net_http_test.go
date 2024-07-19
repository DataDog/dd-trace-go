package namingschematest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var (
	netHTTPServer = testCase{
		name: instrumentation.PackageNetHTTP + "_server",
		genSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
			var opts []httptrace.Option
			if serviceOverride != "" {
				opts = append(opts, httptrace.WithService(serviceOverride))
			}
			mt := mocktracer.Start()
			defer mt.Stop()

			mux := httptrace.NewServeMux(opts...)
			mux.HandleFunc("/200", handler200)
			r := httptest.NewRequest("GET", "http://localhost/200", nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)

			return mt.FinishedSpans()
		},
		wantServiceNameV0: serviceNameAssertions{
			defaults:        []string{"http.router"},
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

	netHTTPClient = testCase{
		name: instrumentation.PackageNetHTTP + "_client",
		genSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
			var opts []httptrace.RoundTripperOption
			if serviceOverride != "" {
				opts = append(opts, httptrace.WithService(serviceOverride))
			}
			mt := mocktracer.Start()
			defer mt.Stop()

			srv := httptest.NewServer(http.HandlerFunc(handler200))
			defer srv.Close()

			c := httptrace.WrapClient(&http.Client{}, opts...)
			req, err := http.NewRequest(http.MethodGet, srv.URL+"/200", nil)
			require.NoError(t, err)
			resp, err := c.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			return mt.FinishedSpans()
		},
		wantServiceNameV0: serviceNameAssertions{
			defaults:        []string{""},
			ddService:       []string{""},
			serviceOverride: []string{testServiceOverride},
		},
		assertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 1)
			assert.Equal(t, "http.request", spans[0].OperationName())
		},
		assertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 1)
			assert.Equal(t, "http.client.request", spans[0].OperationName())
		},
	}
)

func handler200(w http.ResponseWriter, _ *http.Request) {
	w.Write([]byte("OK\n"))
}
