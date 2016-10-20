package gintrace

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func init() {
	gin.SetMode(gin.ReleaseMode) // silence annoying log msgs
}

func TestTrace200(t *testing.T) {
	assert := assert.New(t)

	transport := &dummyTransport{}
	testTracer := getTestTracer(transport)

	middleware := NewMiddlewareTracer("foobar", testTracer)

	router := gin.New()
	router.Use(middleware.Handle)
	router.GET("/user/:id", func(c *gin.Context) {
		// assert we patch the span on the request context.
		span := SpanDefault(c)
		span.SetMeta("test.gin", "ginny")
		assert.Equal(span.Service, "foobar")
		id := c.Param("id")
		c.Writer.Write([]byte(id))
	})

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()

	// do and verify the request
	router.ServeHTTP(w, r)
	response := w.Result()
	assert.Equal(response.StatusCode, 200)

	// verify traces look good
	testTracer.Flush()
	spans := transport.Spans()
	assert.Len(spans, 1)
	if len(spans) < 1 {
		t.Fatalf("no spans")
	}
	s := spans[0]
	assert.Equal(s.Service, "foobar")
	assert.Equal(s.Name, "gin.request")
	// FIXME[matt] would be much nicer to have "/user/:id" here
	assert.True(strings.Contains(s.Resource, "gintrace.TestTrace200"))
	assert.Equal(s.GetMeta("test.gin"), "ginny")
	assert.Equal(s.GetMeta("http.status_code"), "200")
	assert.Equal(s.GetMeta("http.method"), "GET")
	assert.Equal(s.GetMeta("http.url"), "/user/123")
}

func TestDisabled(t *testing.T) {
	assert := assert.New(t)

	transport := &dummyTransport{}
	testTracer := getTestTracer(transport)
	testTracer.SetEnabled(false)

	middleware := NewMiddlewareTracer("foobar", testTracer)

	router := gin.New()
	router.Use(middleware.Handle)
	router.GET("/ping", func(c *gin.Context) {
		span, ok := Span(c)
		assert.Nil(span)
		assert.False(ok)
		c.Writer.Write([]byte("ok"))
	})

	r := httptest.NewRequest("GET", "/ping", nil)
	w := httptest.NewRecorder()

	// do and verify the request
	router.ServeHTTP(w, r)
	response := w.Result()
	assert.Equal(response.StatusCode, 200)

	// verify traces look good
	testTracer.Flush()
	spans := transport.Spans()
	assert.Len(spans, 0)
}

func TestError(t *testing.T) {
	assert := assert.New(t)

	// setup
	testTransport := &dummyTransport{}
	testTracer := getTestTracer(testTransport)
	middleware := NewMiddlewareTracer("foobar", testTracer)
	router := gin.New()
	router.Use(middleware.Handle)

	// a handler with an error and make the requests
	router.GET("/err", func(c *gin.Context) {
		c.AbortWithError(500, errors.New("oh no"))
	})
	r := httptest.NewRequest("GET", "/err", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	response := w.Result()
	assert.Equal(response.StatusCode, 500)

	// verify the errors and status are correct
	testTracer.Flush()
	spans := testTransport.Spans()
	assert.Len(spans, 1)
	if len(spans) < 1 {
		t.Fatalf("no spans")
	}
	s := spans[0]
	assert.Equal(s.Service, "foobar")
	assert.Equal(s.Name, "gin.request")
	assert.Equal(s.GetMeta("http.status_code"), "500")
	assert.Equal(s.GetMeta(ext.ErrorMsg), "oh no")
	assert.Equal(s.Error, 1)
}

func TestGetSpanNotInstrumented(t *testing.T) {
	assert := assert.New(t)
	router := gin.New()
	router.GET("/ping", func(c *gin.Context) {

		// Assert we don't have a span on the context.
		s, ok := Span(c)
		assert.False(ok)
		assert.Nil(s)
		s = SpanDefault(c)
		assert.Equal(s.Service, "")

		c.Writer.Write([]byte("ok"))
	})
	r := httptest.NewRequest("GET", "/ping", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	response := w.Result()
	assert.Equal(response.StatusCode, 200)
}

func getTestTracer(transport tracer.Transport) *tracer.Tracer {
	testTracer := tracer.NewTracerTransport(transport)
	return testTracer
}

// dummyTransport is a transport that just buffers spans.
type dummyTransport struct {
	spans []*tracer.Span
}

func (d *dummyTransport) Send(s []*tracer.Span) error {
	d.spans = append(d.spans, s...)
	return nil
}

func (d *dummyTransport) Spans() []*tracer.Span {
	s := d.spans
	d.spans = nil
	return s
}
