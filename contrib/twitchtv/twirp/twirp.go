// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package twirp provides tracing functions for tracing clients and servers generated
// by the twirp framework (https://github.com/twitchtv/twirp).
package twirp // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/twitchtv/twirp"

import (
	"net/http"

	v2 "github.com/DataDog/dd-trace-go/contrib/twitchtv/twirp/v2"

	"github.com/twitchtv/twirp"
)

// HTTPClient is duplicated from twirp's generated service code.
// It is declared in this package so that the client can be wrapped
// to initiate traces.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// WrapClient wraps an HTTPClient to add distributed tracing to its requests.
func WrapClient(c HTTPClient, opts ...Option) HTTPClient {
	return v2.WrapClient(c, opts...)
}

// WrapServer wraps an http.Handler to add distributed tracing to a Twirp server.
func WrapServer(h http.Handler, opts ...Option) http.Handler {
	return v2.WrapServer(h, opts...)
}

// NewServerHooks creates the callback hooks for a twirp server to perform tracing.
// It is used in conjunction with WrapServer.
func NewServerHooks(opts ...Option) *twirp.ServerHooks {
	return v2.NewServerHooks(opts...)
}
