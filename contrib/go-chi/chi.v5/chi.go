// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package chi provides tracing functions for tracing the go-chi/chi/v5 package (https://github.com/go-chi/chi).
package chi // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/go-chi/chi.v5"

import (
	"net/http"

	v2 "github.com/DataDog/dd-trace-go/contrib/go-chi/chi.v5/v2"
)

// Middleware returns middleware that will trace incoming requests.
func Middleware(opts ...Option) func(next http.Handler) http.Handler {
	return v2.Middleware(opts...)
}
