package echo

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
	"github.com/DataDog/dd-trace-go/tracer/tracertest"
	"github.com/labstack/echo"
	"github.com/stretchr/testify/assert"
)

func init() {

}

func TestChildSpan(t *testing.T) {
	assert := assert.New(t)
	testTracer, _ := tracertest.GetTestTracer()
	tracer.DefaultTracer = testTracer

	router := echo.New()
	router.Use(Middleware("foobar"))
	router.GET("/user/:id", func(c echo.Context) error {
		_, ok := tracer.SpanFromContext(c.Request().Context())
		assert.True(ok)
		return nil
	})

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, r)
}

func TestTrace200(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := tracertest.GetTestTracer()
	tracer.DefaultTracer = testTracer

	router := echo.New()
	router.Use(Middleware("foobar"))
	router.GET("/user/:id", func(c echo.Context) error {
		// assert we patch the span on the request context.
		span, ok := tracer.SpanFromContext(c.Request().Context())
		assert.True(ok)
		span.SetMeta("test.echo", "echony")
		assert.Equal(span.Service, "foobar")
		id := c.Param("id")
		return c.String(200, id)
	})

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()

	// do and verify the request
	router.ServeHTTP(w, r)
	response := w.Result()
	assert.Equal(response.StatusCode, 200)

	// verify traces look good
	testTracer.ForceFlush()
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)
	if len(spans) < 1 {
		t.Fatalf("no spans")
	}
	s := spans[0]
	assert.Equal(s.Service, "foobar")
	assert.Equal(s.Name, "http.request")
	// FIXME[matt] would be much nicer to have "/user/:id" here
	assert.Equal(s.Resource, "/user/:id")
	assert.Equal(s.GetMeta("test.echo"), "echony")
	assert.Equal(s.GetMeta("http.status_code"), "200")
	assert.Equal(s.GetMeta("http.method"), "GET")
	assert.Equal(s.GetMeta("http.url"), "/user/123")
}

func TestDisabled(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := tracertest.GetTestTracer()
	testTracer.SetEnabled(false)
	tracer.DefaultTracer = testTracer

	router := echo.New()
	router.Use(Middleware("foobar"))
	router.GET("/ping", func(c echo.Context) error {
		_, ok := tracer.SpanFromContext(c.Request().Context())
		assert.False(ok)
		return c.String(200, "ok")
	})

	r := httptest.NewRequest("GET", "/ping", nil)
	w := httptest.NewRecorder()

	// do and verify the request
	router.ServeHTTP(w, r)
	response := w.Result()
	assert.Equal(response.StatusCode, 200)

	// verify traces look good
	testTracer.ForceFlush()
	spans := testTransport.Traces()
	assert.Len(spans, 0)
}

func TestError(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := tracertest.GetTestTracer()
	tracer.DefaultTracer = testTracer

	// setup
	router := echo.New()
	router.Use(Middleware("foobar"))

	// a handler with an error and make the requests
	router.GET("/err", func(c echo.Context) error {
		err := errors.New("oh no")
		c.Error(err)
		return err
	})
	r := httptest.NewRequest("GET", "/err", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	response := w.Result()
	assert.Equal(response.StatusCode, 500)

	// verify the errors and status are correct
	testTracer.ForceFlush()
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)
	if len(spans) < 1 {
		t.Fatalf("no spans")
	}
	s := spans[0]
	assert.Equal(s.Service, "foobar")
	assert.Equal(s.Name, "http.request")
	assert.Equal(s.GetMeta(ext.HTTPCode), "500")
	assert.Equal(s.GetMeta(ext.ErrorMsg), "oh no")
	assert.Equal(s.Error, int32(1))
}

func TestGetSpanNotInstrumented(t *testing.T) {
	assert := assert.New(t)
	router := echo.New()
	router.GET("/ping", func(c echo.Context) error {
		// Assert we don't have a span on the context.
		_, ok := tracer.SpanFromContext(c.Request().Context())
		assert.False(ok)
		return c.String(200, "ok")
	})
	r := httptest.NewRequest("GET", "/ping", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	response := w.Result()
	assert.Equal(response.StatusCode, 200)
}
