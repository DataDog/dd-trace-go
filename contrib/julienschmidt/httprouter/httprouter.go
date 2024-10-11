// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package httprouter provides functions to trace the julienschmidt/httprouter package (https://github.com/julienschmidt/httprouter).
package httprouter // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/julienschmidt/httprouter"

import (
	"net/http"

	v2 "github.com/DataDog/dd-trace-go/contrib/julienschmidt/httprouter/v2"
)

// Router is a traced version of httprouter.Router.
type Router struct {
	*v2.Router
}

// New returns a new router augmented with tracing.
func New(opts ...RouterOption) *Router {
	r := v2.New(opts...)
	return &Router{r}
}

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.Router.ServeHTTP(w, req)
}
