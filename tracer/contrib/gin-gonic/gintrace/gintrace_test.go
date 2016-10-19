package gintrace

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

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
