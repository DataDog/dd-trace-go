// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package mux provides tracing functions for tracing the gorilla/mux package (https://github.com/gorilla/mux).
package mux // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/gorilla/mux"

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/gorilla/mux/v2"

	"github.com/gorilla/mux"
)

// Router registers routes to be matched and dispatches a handler.
type Router = v2.Router

// NewRouter returns a new router instance traced with the global tracer.
func NewRouter(opts ...RouterOption) *Router {
	return WrapRouter(mux.NewRouter(), opts...)
}

// WrapRouter returns the given router wrapped with the tracing of the HTTP
// requests and responses served by the router.
func WrapRouter(router *mux.Router, opts ...RouterOption) *Router {
	return v2.WrapRouter(router, opts...)
}
