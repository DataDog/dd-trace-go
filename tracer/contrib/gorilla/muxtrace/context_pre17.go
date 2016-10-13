// +build !go1.7

package muxtrace

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/gorilla/context"
)

type key int

const spanKey key = 0

// SetRequestSpan sets the span on the request's context. Under the hood,
// it will use request.Context() if it's available, otherwise falling back
// to using gorilla/context.
func SetRequestSpan(r *http.Request, span *tracer.Span) *http.Request {
	if r == nil || span == nil {
		return r
	}

	context.Set(r, spanKey, span)
	return r
}

// GetRequestSpan will return the span associated with the given request. It
// will return nil/false if it doesn't exist.
func GetRequestSpan(r *http.Request) (span *tracer.Span, ok bool) {
	if s := context.Get(r, spanKey); s != nil {
		span, ok = s.(*tracer.Span)
		return span, ok
	}

	return nil, false
}
