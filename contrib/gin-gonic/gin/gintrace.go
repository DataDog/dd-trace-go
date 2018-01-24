// Package gin provides functions to trace the gin-gonic/gin package (https://github.com/gin-gonic/gin).
package gin

import (
	"fmt"
	"strconv"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
	"github.com/gin-gonic/gin"
)

const spanKey = "dd-trace-span"

// Trace returns middleware that will trace incoming requests.
// The last parameter is optional and can be used to pass a custom tracer.
func Middleware(service string) gin.HandlerFunc {
	// TODO(gbbr): Handle this when we switch to OpenTracing.
	t := tracer.DefaultTracer
	t.SetServiceInfo(service, "gin-gonic/gin", ext.AppTypeWeb)
	return func(c *gin.Context) {
		// bail out if tracing isn't enabled
		if !t.Enabled() {
			c.Next()
			return
		}

		resource := c.HandlerName()
		// TODO(x): get the span from the request context
		span := t.NewRootSpan("http.request", service, resource)
		defer span.Finish()

		span.Type = ext.HTTPType
		span.SetMeta(ext.HTTPMethod, c.Request.Method)
		span.SetMeta(ext.HTTPURL, c.Request.URL.Path)

		// pass the span through the request context
		c.Set(spanKey, span)

		// serve the request to the next middleware
		c.Next()

		span.SetMeta(ext.HTTPCode, strconv.Itoa(c.Writer.Status()))

		if len(c.Errors) > 0 {
			span.SetMeta("gin.errors", c.Errors.String())
			span.SetError(c.Errors[0])
		}
	}
}

// Span returns the Span stored in the given Context, otherwise nil.
func SpanFromContext(c *gin.Context) *tracer.Span {
	s, ok := c.Get(spanKey)
	if !ok {
		return nil
	}
	span, ok := s.(*tracer.Span)
	if !ok {
		return nil
	}
	return span
}

// HTML will trace the rendering of the template as a child of the span in the given context.
func HTML(c *gin.Context, code int, name string, obj interface{}) {
	span := SpanFromContext(c)
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

	c.HTML(code, name, obj)
}
