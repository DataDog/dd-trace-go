// Package chi provides tracing middleware for the Chi web framework.
package chi

import (
	"net/http"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http"
	"github.com/DataDog/dd-trace-go/tracer"
)

func Middleware(service, resource string, trc *tracer.Tracer) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return httptrace.WrapHandlerWithTracer(next, service, resource, trc)
	}
}
