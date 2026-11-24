// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package httprouter

import (
	"context"

	"github.com/julienschmidt/httprouter"
)

type traceMarkKey struct{}

// traceMark tags ctx as a request this package serves through router.
// [Router.ServeHTTP] calls it in otelc builds only.
func traceMark(ctx context.Context, router *httprouter.Router) context.Context {
	return context.WithValue(ctx, traceMarkKey{}, router)
}

// TraceMarked reports whether ctx is a request this package already serves
// through router.
//
// The otelc rules hook httprouter.Router.ServeHTTP, which [Router.ServeHTTP]
// calls itself, and mark the requests [Router.ServeHTTP] serves. The mark tells
// those two apart, so a router wrapped with this
// package is traced once in an otelc build rather than twice. A different
// router, such as one used as a handler of this one, is not marked.
func TraceMarked(ctx context.Context, router *httprouter.Router) bool {
	marked, _ := ctx.Value(traceMarkKey{}).(*httprouter.Router)
	return marked != nil && marked == router
}
