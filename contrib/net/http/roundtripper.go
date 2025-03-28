// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	"net/http"

	internal "github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"
	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/wrap"
)

type roundTripper struct {
	base http.RoundTripper
	cfg  *internal.RoundTripperConfig
}

func (rt *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req, after, err := wrap.ObserveRoundTrip(rt.cfg, req)
	if err != nil {
		return nil, err
	}
	resp, err := rt.base.RoundTrip(req)
	return after(resp, err)
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
