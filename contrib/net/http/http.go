// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package http provides functions to trace the net/http package (https://golang.org/pkg/net/http).
package http // import "github.com/DataDog/dd-trace-go/contrib/net/http/v2"

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/wrap"
)

type ServeMux = wrap.ServeMux

// NewServeMux allocates and returns an http.ServeMux augmented with the
// global tracer.
func NewServeMux(opts ...Option) *ServeMux {
	return wrap.NewServeMux(opts...)
}

// WrapHandler wraps an http.Handler with tracing using the given service and resource.
// If the WithResourceNamer option is provided as part of opts, it will take precedence over the resource argument.
func WrapHandler(h http.Handler, service, resource string, opts ...Option) http.Handler {
	return wrap.Handler(h, service, resource, opts...)
}
