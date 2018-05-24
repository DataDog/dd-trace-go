package httputil

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
)

func TestTraceAndServe(t *testing.T) {
	t.Run("regular", func(t *testing.T) {
		mt := mocktracer.Start()
		assert := assert.New(t)
		defer mt.Stop()

		w := httptest.NewRecorder()
		r, err := http.NewRequest("GET", "/", nil)
		assert.NoError(err)
		handler := func(w http.ResponseWriter, r *http.Request) {
			_, ok := w.(http.Hijacker)
			assert.False(ok)
			_, ok = w.(*responseWriter)
			assert.True(ok)
			http.Error(w, "some error", http.StatusServiceUnavailable)
		}
		TraceAndServe(http.HandlerFunc(handler), w, r, "service", "resource")
		spans := mt.FinishedSpans()
		span := spans[0]

		assert.Len(spans, 1)
		assert.Equal(ext.AppTypeWeb, span.Tag(ext.SpanType))
		assert.Equal("service", span.Tag(ext.ServiceName))
		assert.Equal("resource", span.Tag(ext.ResourceName))
		assert.Equal("GET", span.Tag(ext.HTTPMethod))
		assert.Equal("/", span.Tag(ext.HTTPURL))
		assert.Equal("503", span.Tag(ext.HTTPCode))
		assert.Equal("503: Service Unavailable", span.Tag(ext.Error).(error).Error())
	})

	t.Run("hijackable", func(t *testing.T) {
		assert := assert.New(t)
		handler := func(w http.ResponseWriter, r *http.Request) {
			_, ok := w.(http.Hijacker)
			assert.True(ok)
			_, ok = w.(*hijackableResponseWriter)
			assert.True(ok)
			fmt.Fprintln(w, "Hello, world!")
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			TraceAndServe(http.HandlerFunc(handler), w, r, "service", "resource")
		}))
		defer srv.Close()

		res, err := http.Get(srv.URL)
		assert.NoError(err)
		slurp, err := ioutil.ReadAll(res.Body)
		res.Body.Close()
		assert.NoError(err)
		assert.Equal("Hello, world!\n", string(slurp))
	})
}
