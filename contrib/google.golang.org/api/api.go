// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

// Package api provides functions to trace the google.golang.org/api package.
package api // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/api"

//go:generate go run make_endpoints.go

import (
	"math"
	"net/http"

	"golang.org/x/oauth2/google"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/api/internal"
	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
)

// apiEndpoints are all of the defined endpoints for the Google API; it is populated
// by "go generate".
var apiEndpoints *internal.Tree

// NewClient creates a new oauth http client suitable for use with the google
// APIs with all requests traced automatically.
func NewClient(options ...Option) (*http.Client, error) {
	cfg := newConfig(options...)
	client, err := google.DefaultClient(cfg.ctx, cfg.scopes...)
	if err != nil {
		return nil, err
	}
	client.Transport = WrapRoundTripper(client.Transport, options...)
	return client, nil
}

// WrapRoundTripper wraps a RoundTripper intended for interfacing with
// Google APIs and traces all requests.
func WrapRoundTripper(transport http.RoundTripper, options ...Option) http.RoundTripper {
	cfg := newConfig(options...)
	rtOpts := []httptrace.RoundTripperOption{
		httptrace.WithBefore(func(req *http.Request, span ddtrace.Span) {
			e, ok := apiEndpoints.Get(req.URL.Hostname(), req.Method, req.URL.Path)
			if ok {
				span.SetTag(ext.ServiceName, e.ServiceName)
				span.SetTag(ext.ResourceName, e.ResourceName)
			} else {
				span.SetTag(ext.ServiceName, "google")
				span.SetTag(ext.ResourceName, req.Method+" "+req.URL.Hostname())
			}
			if cfg.serviceName != "" {
				span.SetTag(ext.ServiceName, cfg.serviceName)
			}
		}),
	}
	if !math.IsNaN(cfg.analyticsRate) {
		rtOpts = append(rtOpts, httptrace.RTWithAnalyticsRate(cfg.analyticsRate))
	}
	return httptrace.WrapRoundTripper(transport, rtOpts...)
}
