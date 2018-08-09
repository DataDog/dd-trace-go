package api

//go:generate go run make_endpoints.go

import (
	"net/http"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
)

// WrapRoundTripper wraps a RoundTripper intended for interfacing with
// Google APIs and traces all requests.
func WrapRoundTripper(transport http.RoundTripper) http.RoundTripper {
	return httptrace.WrapRoundTripper(transport,
		httptrace.WithBefore(func(req *http.Request, span ddtrace.Span) {
			e, ok := APIEndpoints.Get(req.URL.Hostname(), req.Method, req.URL.Path)
			if ok {
				span.SetTag(ext.ServiceName, e.ServiceName)
				span.SetTag(ext.ResourceName, e.ResourceName)
			} else {
				span.SetTag(ext.ServiceName, "google")
				span.SetTag(ext.ResourceName, req.Method+" "+req.URL.Hostname())
			}
		}))
}
