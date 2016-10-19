// Package gintrace provides tracing middleware for the Gin web framework.
package gintrace

import (
	"strconv"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
	"github.com/gin-gonic/gin"
)

// Key is the string that we'll use to store spans in the tracer. Override it
// you want.
var Key = "datadog_trace_span"

// Middleware is a tracing middleware for the Gin framework.
type Middleware struct {
	service string
	trc     *tracer.Tracer
}

// NewMiddleware creates a Middleware that will trace the given service with
// the default tracer.
func NewMiddleware(service string) *Middleware {
	return NewMiddlewareTracer(service, tracer.DefaultTracer)
}

// NewMiddlewareTracer creates a new Middleware that will trace the given
// service with the given tracer.
func NewMiddlewareTracer(service string, trc *tracer.Tracer) *Middleware {
	return &Middleware{
		service: service,
		trc:     trc,
	}
}

// Handle is a gin HandlerFunc that will add tracing to the given request.
func (m *Middleware) Handle(c *gin.Context) {
	// FIXME[matt] the handler name is a bit unwieldy and uses reflection
	// under the hood. might be better to tackle this task and do it right
	// so we can end up with "user/:user/whatever" instead of
	// "github.com/foobar/blah"
	//.
	// "github.com/foobar/blah"onic/gin/issues/649
	resource := c.HandlerName()

	// Create our span and patch it to the context for downstream.
	span := m.trc.NewRootSpan("gin.request", m.service, resource)
	c.Set(Key, span)

	// Pass along.
	c.Next()

	// wrap it up.
	span.SetMeta(ext.HTTPCode, strconv.Itoa(c.Writer.Status()))
	span.SetMeta(ext.HTTPMethod, c.Request.Method)
	span.Finish()
}

// Span returns the Span stored in the given Context and true. If it doesn't exist,
// it will returns (nil, false)
func Span(c *gin.Context) (*tracer.Span, bool) {
	if c == nil {
		return nil, false
	}

	s, ok := c.Get(Key)
	if !ok {
		return nil, false
	}
	switch span := s.(type) {
	case *tracer.Span:
		return span, true
	}

	return nil, false
}

// SpanDefault returns the span stored in the given Context. If none exists,
// it will return an empty span.
func SpanDefault(c *gin.Context) *tracer.Span {
	span, ok := Span(c)
	if !ok {
		return &tracer.Span{}
	}
	return span
}
