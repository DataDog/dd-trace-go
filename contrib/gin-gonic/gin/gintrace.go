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
		span, ctx := t.NewChildSpanWithContext("http.request", c.Request.Context())
		defer span.Finish()

		span.Service = service
		span.Resource = resource
		span.Type = ext.HTTPType
		span.SetMeta(ext.HTTPMethod, c.Request.Method)
		span.SetMeta(ext.HTTPURL, c.Request.URL.Path)

		// pass the span through the request context
		c.Request = c.Request.WithContext(ctx)

		// serve the request to the next middleware
		c.Next()

		span.SetMeta(ext.HTTPCode, strconv.Itoa(c.Writer.Status()))

		if len(c.Errors) > 0 {
			span.SetMeta("gin.errors", c.Errors.String())
			span.SetError(c.Errors[0])
		}
	}
}

// HTML will trace the rendering of the template as a child of the span in the given context.
func HTML(c *gin.Context, code int, name string, obj interface{}) {
	t := tracer.DefaultTracer
	if !t.Enabled() {
		c.HTML(code, name, obj)
		return
	}

	span := t.NewChildSpanFromContext("gin.render.html", c.Request.Context())
	span.SetMeta("go.template", name)
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("error rendering tmpl:%s: %s", name, r)
			span.FinishWithErr(err)
			panic(r)
		} else {
			span.Finish()
		}
	}()

	c.HTML(code, name, obj)
}
