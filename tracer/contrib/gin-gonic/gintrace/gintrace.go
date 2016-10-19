package gintrace

import (
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/gin-gonic/gin"
)

// Key is the string that we'll use to store spans in the tracer. Override it
// you want.
var Key = "datadog_trace_span"

type Middleware struct {
	service string
	trc     *tracer.Tracer
}

func NewMiddleware(service string, trc *tracer.Tracer) *Middleware {
	return &Middleware{service, trc}
}

func (m *Middleware) Handle(c *gin.Context) {
	// FIXME[matt] the handler name is a bit unwieldy and uses reflection
	// under the hood. might be better to tackle and do it right
	// https://github.com/gin-gonic/gin/issues/649
	span := m.trc.NewRootSpan("gin.request", m.service, c.HandlerName())
	defer span.Finish()

	c.Set(Key, span)
	c.Next()
}

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
	default:
		return nil, false
	}
}

func SpanDefault(c *gin.Context) *tracer.Span {
	span, ok := Span(c)
	if !ok {
		return &tracer.Span{}
	}
	return span
}
