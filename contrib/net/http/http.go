// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package http provides functions to trace the net/http package (https://golang.org/pkg/net/http).
package http // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"

import (
	"net/http"

	v2 "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
)

// ServeMux is an HTTP request multiplexer that traces all the incoming requests.
type ServeMux struct {
	*v2.ServeMux
}

func NewServeMux(opts ...Option) *ServeMux {
	m := v2.NewServeMux(opts...)
	return &ServeMux{m}
}

// ServeHTTP dispatches the request to the handler
// whose pattern most closely matches the request URL.
// We only need to rewrite this function to be able to trace
// all the incoming requests to the underlying multiplexer
func (mux *ServeMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mux.ServeMux.ServeHTTP(w, r)
}

// WrapHandler wraps an http.Handler with tracing using the given service and resource.
// If the WithResourceNamer option is provided as part of opts, it will take precedence over the resource argument.
func WrapHandler(h http.Handler, service, resource string, opts ...Option) http.Handler {
	return v2.WrapHandler(h, service, resource, opts...)
}
