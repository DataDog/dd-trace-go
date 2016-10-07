// +build go1.7

package muxtrace

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/tracer"
)

func setOnRequestContext(r *http.Request, span *tracer.Span) *http.Request {
	if r == nil || span == nil {
		return r
	}

	ctx := tracer.ContextWithSpan(r.Context(), span)
	return r.WithContext(ctx)
}
