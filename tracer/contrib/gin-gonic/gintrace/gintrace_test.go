package gintrace

import (
	"errors"
	"fmt"
	"html/template"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
	"github.com/DataDog/dd-trace-go/tracer/test"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func init() {
	gin.SetMode(gin.ReleaseMode) // silence annoying log msgs
}

func TestChildSpan(t *testing.T) {
	assert := assert.New(t)
	testTracer, _ := test.GetTestTracer()

	middleware := newMiddleware("foobar", testTracer)

	router := gin.New()
	router.Use(middleware.Handle)
	router.GET("/user/:id", func(c *gin.Context) {
		span, ok := tracer.SpanFromContext(c)
		assert.True(ok)
		assert.NotNil(span)
	})

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, r)
}

func TestTrace200(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := test.GetTestTracer()

	middleware := newMiddleware("foobar", testTracer)

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
	assert.Nil(testTracer.FlushTraces())
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
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
	testTracer, testTransport := test.GetTestTracer()
	testTracer.SetEnabled(false)

	middleware := newMiddleware("foobar", testTracer)

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
	testTracer.FlushTraces()
	spans := testTransport.Traces()
	assert.Len(spans, 0)
}

func TestError(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := test.GetTestTracer()

	// setup
	middleware := newMiddleware("foobar", testTracer)
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
	testTracer.FlushTraces()
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 1)
	if len(spans) < 1 {
		t.Fatalf("no spans")
	}
	s := spans[0]
	assert.Equal(s.Service, "foobar")
	assert.Equal(s.Name, "gin.request")
	assert.Equal(s.GetMeta("http.status_code"), "500")
	assert.Equal(s.GetMeta(ext.ErrorMsg), "oh no")
	assert.Equal(s.Error, int32(1))
}

func TestHTML(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := test.GetTestTracer()

	// setup
	middleware := newMiddleware("tmplservice", testTracer)

	router := gin.New()
	router.Use(middleware.Handle)

	// add a template
	tmpl := template.Must(template.New("hello").Parse("hello {{.}}"))
	router.SetHTMLTemplate(tmpl)

	// a handler with an error and make the requests
	router.GET("/hello", func(c *gin.Context) {
		HTML(c, 200, "hello", "world")
	})
	r := httptest.NewRequest("GET", "/hello", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	response := w.Result()
	assert.Equal(response.StatusCode, 200)
	assert.Equal("hello world", w.Body.String())

	// verify the errors and status are correct
	testTracer.FlushTraces()
	traces := testTransport.Traces()
	assert.Len(traces, 1)
	spans := traces[0]
	assert.Len(spans, 2)
	for _, s := range spans {
		assert.Equal(s.Service, "tmplservice")
	}

	tspan := spans[0]
	assert.Equal(tspan.Name, "gin.render.html")
	assert.Equal(tspan.GetMeta("go.template"), "hello")
	fmt.Println(spans)
}

func TestGetSpanNotInstrumented(t *testing.T) {
	assert := assert.New(t)
	router := gin.New()
	router.GET("/ping", func(c *gin.Context) {
		// Assert we don't have a span on the context.
		s, ok := Span(c)
		assert.False(ok)
		assert.Nil(s)
		// and the default span is empty
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
