package mux

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	"github.com/stretchr/testify/assert"
)

var httpTests = []struct {
	responseCode int
	httpMethod   string
	url          string
}{
	{http.StatusOK, "GET", "/200"},
	{http.StatusNotFound, "GET", "/not_a_real_route"},
	{http.StatusMethodNotAllowed, "POST", "/405"},
	{http.StatusInternalServerError, "GET", "/500"},
}

func TestHttpTracer(t *testing.T) {
	for _, ht := range httpTests {
		t.Run(ht.httpMethod+ht.url, func(t *testing.T) {
			assert := assert.New(t)
			mt := mocktracer.Start()
			defer mt.Stop()
			responseCodeStr := strconv.Itoa(ht.responseCode)

			// Send and verify a request
			r := httptest.NewRequest(ht.httpMethod, ht.url, nil)
			w := httptest.NewRecorder()
			router().ServeHTTP(w, r)
			assert.Equal(ht.responseCode, w.Code)
			assert.Equal(responseCodeStr+"!\n", w.Body.String())

			spans := mt.FinishedSpans()
			assert.Equal(1, len(spans))

			s := spans[0]
			assert.Equal("http.request", s.OperationName())
			assert.Equal("my-service", s.Tag(ext.ServiceName))
			assert.Equal(responseCodeStr, s.Tag(ext.HTTPCode))
			assert.Equal(ht.httpMethod, s.Tag(ext.HTTPMethod))
			assert.Equal(ht.url, s.Tag(ext.HTTPURL))

			// Response code dependant tests
			switch ht.responseCode {
			case http.StatusInternalServerError:
				assert.Equal(ht.httpMethod+" "+ht.url, s.Tag(ext.ResourceName))
				assert.Equal("500: Internal Server Error", s.Tag(ext.Error).(error).Error())

			case http.StatusNotFound, http.StatusMethodNotAllowed:
				assert.Equal(ht.httpMethod+" unknown", s.Tag(ext.ResourceName))

			default:
				assert.Equal(ht.httpMethod+" "+ht.url, s.Tag(ext.ResourceName))
			}
		})
	}
}

func router() http.Handler {
	mux := NewRouter(WithServiceName("my-service"))
	mux.Handle("/200", okHandler())
	mux.Handle("/500", errorHandler(http.StatusInternalServerError))
	mux.Handle("/405", okHandler()).Methods("GET")
	mux.NotFoundHandler = errorHandler(http.StatusNotFound)
	mux.MethodNotAllowedHandler = errorHandler(http.StatusMethodNotAllowed)
	return mux
}

func errorHandler(code int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, fmt.Sprintf("%d!", code), code)
	})
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("200!\n"))
	})
}
