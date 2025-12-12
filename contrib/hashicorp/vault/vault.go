// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package vault contains functions to construct or augment an [*http.Client] that
// will integrate with the [github.com/hashicorp/vault/api] and collect traces to
// send to Datadog.
//
// The easiest way to use this package is to create an [*http.Client] with
// [NewHTTPClient], and put it in the Vault [api.Config] that is passed to
// [api.NewClient].
//
// If you are already using your own [*http.Client] with the Vault API, you can
// use the [WrapHTTPClient] function to wrap the client with the tracer code.
// Your [*http.Client] will continue to work as before, but will also capture
// traces.
package vault

import (
	"fmt"
	"net"
	"net/http"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"github.com/hashicorp/vault/api"
	"github.com/hashicorp/vault/sdk/helper/consts"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageHashicorpVaultAPI)
}

// NewHTTPClient returns an [*http.Client] for use in the Vault API config
// [*api.Client]. A set of options can be passed in for further configuration.
func NewHTTPClient(opts ...Option) *http.Client {
	dc := api.DefaultConfig()
	c := dc.HttpClient
	WrapHTTPClient(c, opts...)
	return c
}

// WrapHTTPClient takes an existing [*http.Client] and wraps the underlying
// transport with tracing.
func WrapHTTPClient(c *http.Client, opts ...Option) *http.Client {
	if c.Transport == nil {
		c.Transport = http.DefaultTransport
	}
	var conf config
	defaults(&conf)
	for _, o := range opts {
		o.apply(&conf)
	}
	c.Transport = httptrace.WrapRoundTripper(c.Transport,
		httptrace.WithAnalyticsRate(conf.analyticsRate),
		httptrace.WithSpanNamer(func(_ *http.Request) string {
			return conf.spanName
		}),
		httptrace.WithBefore(func(r *http.Request, s *tracer.Span) {
			s.SetTag(ext.ServiceName, conf.serviceName)
			s.SetTag(ext.HTTPURL, r.URL.Path)
			s.SetTag(ext.HTTPMethod, r.Method)
			s.SetTag(ext.ResourceName, r.Method+" "+r.URL.Path)
			s.SetTag(ext.SpanType, ext.SpanTypeHTTP)
			s.SetTag(ext.Component, "hashicorp/vault")
			s.SetTag(ext.SpanKind, ext.SpanKindClient)
			if host, _, err := net.SplitHostPort(r.Host); err == nil {
				s.SetTag(ext.NetworkDestinationName, host)
			}

			if ns := r.Header.Get(consts.NamespaceHeaderName); ns != "" {
				s.SetTag("vault.namespace", ns)
			}
		}),
		httptrace.WithAfter(func(res *http.Response, s *tracer.Span) {
			if res == nil {
				// An error occurred during the request.
				return
			}
			s.SetTag(ext.HTTPCode, res.StatusCode)
			if res.StatusCode >= 400 {
				s.SetTag(ext.Error, true)
				s.SetTag(ext.ErrorMsg, fmt.Sprintf("%d: %s", res.StatusCode, http.StatusText(res.StatusCode)))
			}
		}),
	)
	return c
}
