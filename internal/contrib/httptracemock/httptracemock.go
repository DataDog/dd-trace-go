package httptracemock

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/internal/contrib/httptrace"
)

// ServeMux is an HTTP request multiplexer that traces all the incoming requests.
type ServeMux struct {
	*http.ServeMux
	spanOpts []tracer.StartSpanOption
}

// NewServeMux allocates and returns an http.ServeMux augmented with the
// global tracer.
func NewServeMux() *ServeMux {
	spanOpts := []tracer.StartSpanOption{
		tracer.Tag(ext.SpanKind, ext.SpanKindServer),
		tracer.Tag(ext.Component, "net/http"),
	}
	return &ServeMux{
		ServeMux: http.NewServeMux(),
		spanOpts: spanOpts,
	}
}

// ServeHTTP dispatches the request to the handler
// whose pattern most closely matches the request URL.
// We only need to rewrite this function to be able to trace
// all the incoming requests to the underlying multiplexer
func (mux *ServeMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// get the resource associated to this request
	_, route := mux.Handler(r)

	resource := r.Method + " " + route
	so := make([]tracer.StartSpanOption, len(mux.spanOpts), len(mux.spanOpts)+1)
	copy(so, mux.spanOpts)
	so = append(so, tracer.ResourceName(resource))

	span, ctx := httptrace.StartRequestSpan(r, so...)
	defer func() {
		httptrace.FinishRequestSpan(span, 200)
	}()
	var h http.Handler = mux.ServeMux
	if appsec.Enabled() {
		h = httpsec.WrapHandler(h, span, nil, nil)
	}
	h.ServeHTTP(w, r.WithContext(ctx))
}
