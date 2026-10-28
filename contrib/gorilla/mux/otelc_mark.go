// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package mux

import (
	"context"

	"github.com/gorilla/mux"
)

type traceMarkKey struct{}

// traceMark tags ctx as a request this package serves through router. Only
// otelc builds call it, from code the otelc rules add to [Router.ServeHTTP].
func traceMark(ctx context.Context, router *mux.Router) context.Context {
	return context.WithValue(ctx, traceMarkKey{}, router)
}

// TraceMarked reports whether ctx is a request this package already serves
// through router.
//
// The otelc rules hook mux.Router.ServeHTTP, which [Router.ServeHTTP] calls
// itself, and mark the requests [Router.ServeHTTP] serves. The mark tells those
// two apart, so a router wrapped with this package
// is traced once in an otelc build rather than twice. A different router, such
// as one used as a handler of this one, is not marked.
func TraceMarked(ctx context.Context, router *mux.Router) bool {
	marked, _ := ctx.Value(traceMarkKey{}).(*mux.Router)
	return marked != nil && marked == router
}
