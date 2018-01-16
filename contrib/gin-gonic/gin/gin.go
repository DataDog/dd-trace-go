// Package gin provides tracing middleware for the Gin web framework.
package gin

import (
	"fmt"
	"strconv"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
	"github.com/gin-gonic/gin"
)

// key is the string we use to store the span in the gin context.
var key = "datadog"

// Trace returns middleware that will trace incoming requests.
// The last parameter is optional and can be used to pass a custom tracer.
func Trace(service string, trc ...*tracer.Tracer) gin.HandlerFunc {
	t := getTracer(trc)
	t.SetServiceInfo(service, "gin-gonic/gin", ext.AppTypeWeb)
	return func(c *gin.Context) {
		// bail out if tracing isn't enabled
		if !t.Enabled() {
			c.Next()
			return
		}

		resource := c.HandlerName()

		// TODO: get the span from the request context
		span := t.NewRootSpan("http.request", service, resource)
		defer span.Finish()

		span.Type = ext.HTTPType
		span.SetMeta(ext.HTTPMethod, c.Request.Method)
		span.SetMeta(ext.HTTPURL, c.Request.URL.Path)

		// pass the span through the request context
		c.Set(key, span)

		// serve the request to the next middleware
		c.Next()

		span.SetMeta(ext.HTTPCode, strconv.Itoa(c.Writer.Status()))

		if len(c.Errors) > 0 {
			span.SetMeta("gin.errors", c.Errors.String())
			span.SetError(c.Errors[0])
		}
	}
}

// Span returns the Span stored in the given Context and true.
// If it doesn't exist, it will returns (nil, false).
func SpanFromContext(c *gin.Context) (*tracer.Span, bool) {
	if c == nil {
		return nil, false
	}

	s, ok := c.Get(key)
	if !ok {
		return nil, false
	}

	span, ok := s.(*tracer.Span)
	if !ok {
		return nil, false
	}

	return span, true
}

// HTML will trace the rendering of the template as a child of the span in the given context.
func HTML(c *gin.Context, code int, name string, obj interface{}) {
	span, _ := SpanFromContext(c)
	if span == nil {
		c.HTML(code, name, obj)
		return
	}

	child := span.Tracer().NewChildSpan("gin.render.html", span)
	child.SetMeta("go.template", name)
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("error rendering tmpl:%s: %s", name, r)
			child.FinishWithErr(err)
			panic(r)
		} else {
			child.Finish()
		}
	}()

	// render
	c.HTML(code, name, obj)
}

// getTracer returns either the tracer passed as the last argument or a default tracer.
func getTracer(tracers []*tracer.Tracer) *tracer.Tracer {
	var t *tracer.Tracer
	if len(tracers) == 0 || (len(tracers) > 0 && tracers[0] == nil) {
		t = tracer.DefaultTracer
	} else {
		t = tracers[0]
	}
	return t
}
