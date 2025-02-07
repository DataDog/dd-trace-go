// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	"fmt"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"

	internal "github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
)

type roundTripper struct {
	base http.RoundTripper
	cfg  *internal.RoundTripperConfig
}

func (rt *roundTripper) RoundTrip(req *http.Request) (res *http.Response, err error) {
	if rt.cfg.IgnoreRequest(req) {
		return rt.base.RoundTrip(req)
	}
	resourceName := rt.cfg.ResourceNamer(req)
	spanName := rt.cfg.SpanNamer(req)
	// Make a copy of the URL so we don't modify the outgoing request
	url := *req.URL
	url.User = nil // Do not include userinfo in the HTTPURL tag.
	opts := []tracer.StartSpanOption{
		tracer.SpanType(ext.SpanTypeHTTP),
		tracer.ResourceName(resourceName),
		tracer.Tag(ext.HTTPMethod, req.Method),
		tracer.Tag(ext.HTTPURL, urlFromRequest(req, rt.cfg.QueryString)),
		tracer.Tag(ext.Component, internal.ComponentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(ext.NetworkDestinationName, url.Hostname()),
	}
	if !math.IsNaN(rt.cfg.AnalyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, rt.cfg.AnalyticsRate))
	}
	if rt.cfg.ServiceName != "" {
		opts = append(opts, tracer.ServiceName(rt.cfg.ServiceName))
	}
	if port, err := strconv.Atoi(url.Port()); err == nil {
		opts = append(opts, tracer.Tag(ext.NetworkDestinationPort, port))
	}
	if len(rt.cfg.SpanOpts) > 0 {
		opts = append(opts, rt.cfg.SpanOpts...)
	}
	span, ctx := tracer.StartSpanFromContext(req.Context(), spanName, opts...)
	defer func() {
		if rt.cfg.After != nil {
			rt.cfg.After(res, span)
		}
		if !events.IsSecurityError(err) && (rt.cfg.ErrCheck == nil || rt.cfg.ErrCheck(err)) {
			span.Finish(tracer.WithError(err))
		} else {
			span.Finish()
		}
	}()
	if rt.cfg.Before != nil {
		rt.cfg.Before(req, span)
	}
	r2 := req.Clone(ctx)
	if rt.cfg.Propagation {
		// inject the span context into the http request copy
		err = tracer.Inject(span.Context(), tracer.HTTPHeadersCarrier(r2.Header))
		if err != nil {
			// this should never happen
			fmt.Fprintf(os.Stderr, "contrib/net/http.Roundtrip: failed to inject http headers: %v\n", err)
		}
	}

	if internal.Instrumentation.AppSecRASPEnabled() {
		if err := httpsec.ProtectRoundTrip(ctx, r2.URL.String()); err != nil {
			return nil, err
		}
	}

	res, err = rt.base.RoundTrip(r2)
	if err != nil {
		span.SetTag("http.errors", err.Error())
		if rt.cfg.ErrCheck == nil || rt.cfg.ErrCheck(err) {
			span.SetTag(ext.Error, err)
		}
	} else {
		span.SetTag(ext.HTTPCode, strconv.Itoa(res.StatusCode))
		if rt.cfg.IsStatusError(res.StatusCode) {
			span.SetTag("http.errors", res.Status)
			span.SetTag(ext.Error, fmt.Errorf("%d: %s", res.StatusCode, http.StatusText(res.StatusCode)))
		}
	}
	return res, err
}

// Unwrap returns the original http.RoundTripper.
func (rt *roundTripper) Unwrap() http.RoundTripper {
	return rt.base
}

// WrapRoundTripper returns a new RoundTripper which traces all requests sent
// over the transport.
func WrapRoundTripper(rt http.RoundTripper, opts ...RoundTripperOption) http.RoundTripper {
	if rt == nil {
		rt = http.DefaultTransport
	}
	cfg := newRoundTripperConfig()
	cfg.ApplyOpts(opts...)
	if wrapped, ok := rt.(*roundTripper); ok {
		rt = wrapped.base
	}
	return &roundTripper{
		base: rt,
		cfg:  cfg,
	}
}

// WrapClient modifies the given client's transport to augment it with tracing and returns it.
func WrapClient(c *http.Client, opts ...RoundTripperOption) *http.Client {
	if c.Transport == nil {
		c.Transport = http.DefaultTransport
	}
	c.Transport = WrapRoundTripper(c.Transport, opts...)
	return c
}

// urlFromRequest returns the URL from the HTTP request. The URL query string is included in the return object iff queryString is true
// See https://docs.datadoghq.com/tracing/configure_data_security#redacting-the-query-in-the-url for more information.
func urlFromRequest(r *http.Request, queryString bool) string {
	// Quoting net/http comments about net.Request.URL on server requests:
	// "For most requests, fields other than Path and RawQuery will be
	// empty. (See RFC 7230, Section 5.3)"
	// This is why we don't rely on url.URL.String(), url.URL.Host, url.URL.Scheme, etc...
	var url string
	path := r.URL.EscapedPath()
	scheme := r.URL.Scheme
	if r.TLS != nil {
		scheme = "https"
	}
	if r.Host != "" {
		url = strings.Join([]string{scheme, "://", r.Host, path}, "")
	} else {
		url = path
	}
	// Collect the query string if we are allowed to report it and obfuscate it if possible/allowed
	if queryString && r.URL.RawQuery != "" {
		query := r.URL.RawQuery
		url = strings.Join([]string{url, query}, "?")
	}
	if frag := r.URL.EscapedFragment(); frag != "" {
		url = strings.Join([]string{url, frag}, "#")
	}
	return url
}
