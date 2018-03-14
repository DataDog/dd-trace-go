// Package gin provides functions to trace the gin-gonic/gin package (https://github.com/gin-gonic/gin).
package gin // import "gopkg.in/DataDog/dd-trace-go.v0/contrib/gin-gonic/gin"

import (
	"fmt"
	"strconv"

	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace/tracer"

	"github.com/gin-gonic/gin"
)

// Middleware returns middleware that will trace incoming requests.
// The last parameter is optional and can be used to pass a custom tracer.
func Middleware(service string) gin.HandlerFunc {
	return func(c *gin.Context) {
		resource := c.HandlerName()
		span, ctx := tracer.StartSpanFromContext(c.Request.Context(), "http.request",
			tracer.ServiceName(service),
			tracer.ResourceName(resource),
			tracer.SpanType(ext.AppTypeWeb),
			tracer.Tag(ext.HTTPMethod, c.Request.Method),
			tracer.Tag(ext.HTTPURL, c.Request.URL.Path),
		)
		defer span.Finish()

		// pass the span through the request context
		c.Request = c.Request.WithContext(ctx)

		// serve the request to the next middleware
		c.Next()

		span.SetTag(ext.HTTPCode, strconv.Itoa(c.Writer.Status()))

		if len(c.Errors) > 0 {
			span.SetTag("gin.errors", c.Errors.String())
			span.SetTag(ext.Error, c.Errors[0])
		}
	}
}

// HTML will trace the rendering of the template as a child of the span in the given context.
func HTML(c *gin.Context, code int, name string, obj interface{}) {
	span, _ := tracer.StartSpanFromContext(c.Request.Context(), "gin.render.html")
	span.SetTag("go.template", name)
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("error rendering tmpl:%s: %s", name, r)
			span.Finish(tracer.WithError(err))
			panic(r)
		} else {
			span.Finish()
		}
	}()
	c.HTML(code, name, obj)
}
