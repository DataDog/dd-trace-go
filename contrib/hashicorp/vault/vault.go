// Package vault contains functions to construct or augment an http.Client that
// will integrate with the github.com/hashicorp/vault/api and collect traces to
// send to DataDog.
//
// The easiest way to use this package is to create an http.Client with
// NewHTTPClient, and put it in the Vault API config that is passed to the
//
// If you are already using your own http.Client with the Vault API, you can
// use the WrapHTTPClient function to wrap the client with the tracer code.
// Your http.Client will continue to work as before, but will also capture
// traces.
package vault

import (
	"fmt"
	"net/http"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"

	"github.com/hashicorp/vault/api"
	"github.com/hashicorp/vault/sdk/helper/consts"
)

// NewHTTPClient returns an http.Client for use in the config for the vault api
// Client. Options can be optionally passed in to configure various tracer
// features such as Analytics
func NewHTTPClient(opts ...Option) *http.Client {
	dc := api.DefaultConfig()
	c := dc.HttpClient
	WrapHTTPClient(c, opts...)
	return c
}

// WrapHTTPClient takes an existing http.Client and wraps the transport with
// the tracing code. This will leave the existing Transport in place underneath,
// only adding tracing code around it.
func WrapHTTPClient(c *http.Client, opts ...Option) *http.Client {
	if c.Transport == nil {
		c.Transport = http.DefaultTransport
	}
	var conf config
	defaults(&conf)
	for _, o := range opts {
		o(&conf)
	}
	c.Transport = httptrace.WrapRoundTripper(c.Transport,
		httptrace.RTWithAnalytics(conf.withAnalytics),
		httptrace.RTWithAnalyticsRate(conf.analyticsRate),
		httptrace.WithBefore(func(r *http.Request, s ddtrace.Span) {
			s.SetTag(ext.ServiceName, conf.serviceName)
			s.SetTag(ext.HTTPURL, r.URL.Path)
			s.SetTag(ext.HTTPMethod, r.Method)
			s.SetTag(ext.ResourceName, r.Method+" "+r.URL.Path)
			s.SetTag(ext.SpanType, ext.SpanTypeHTTP)
			if ns := r.Header.Get(consts.NamespaceHeaderName); ns != "" {
				s.SetTag("vault.namespace", ns)
			}
		}),
		httptrace.WithAfter(func(r *http.Response, s ddtrace.Span) {
			if r == nil {
				// An error occurred during the request.
				return
			}
			s.SetTag(ext.HTTPCode, r.StatusCode)
			if r.StatusCode >= 400 {
				s.SetTag(ext.Error, true)
				s.SetTag(ext.ErrorMsg, fmt.Sprintf("%d: %s", r.StatusCode, http.StatusText(r.StatusCode)))
			}
		}),
	)
	return c
}
