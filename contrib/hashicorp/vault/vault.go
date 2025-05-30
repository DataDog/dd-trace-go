// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package vault contains functions to construct or augment an http.Client that
// will integrate with the github.com/hashicorp/vault/api and collect traces to
// send to Datadog.
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
	"net/http"

	v2 "github.com/DataDog/dd-trace-go/contrib/hashicorp/vault/v2"
)

// NewHTTPClient returns an http.Client for use in the Vault API config
// Client. A set of options can be passed in for further configuration.
func NewHTTPClient(opts ...Option) *http.Client {
	return v2.NewHTTPClient(opts...)
}

// WrapHTTPClient takes an existing http.Client and wraps the underlying
// transport with tracing.
func WrapHTTPClient(c *http.Client, opts ...Option) *http.Client {
	return v2.WrapHTTPClient(c, opts...)
}
