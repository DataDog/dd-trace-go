// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package httptreemux provides functions to trace the dimfeld/httptreemux/v5 package (https://github.com/dimfeld/httptreemux).
package httptreemux // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/dimfeld/httptreemux.v5"

import (
	v2 "github.com/DataDog/dd-trace-go/v2/contrib/dimfeld/httptreemux.v5"
)

// Router is a traced version of httptreemux.TreeMux.
type Router = v2.Router

// New returns a new router augmented with tracing.
func New(opts ...RouterOption) *Router {
	return v2.New(opts...)
}

// ContextRouter is a traced version of httptreemux.ContextMux.
type ContextRouter = v2.ContextRouter

// NewWithContext returns a new router augmented with tracing and preconfigured
// to work with context objects. The matched route and parameters are added to
// the context.
func NewWithContext(opts ...RouterOption) *ContextRouter {
	return v2.NewWithContext(opts...)
}
