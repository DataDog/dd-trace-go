// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package river

import "github.com/riverqueue/river/rivertype"

// AddMiddleware returns a copy of mws with a tracing middleware built from opts
// in first position, so that it runs outermost. If mws already holds a
// middleware created by NewMiddleware, mws is returned unchanged, so a client is
// never traced twice. mws itself is never modified.
//
// It accepts any of the middleware slices of river.Config: Middleware, and the
// older JobInsertMiddleware and WorkerMiddleware. Orchestrion uses it to add
// tracing to every river.Client; applications should prefer adding
// NewMiddleware to river.Config.Middleware.
func AddMiddleware[T rivertype.Middleware](mws []T, opts ...Option) []T {
	for _, mw := range mws {
		if _, ok := any(mw).(*Middleware); ok {
			return mws
		}
	}
	traced, ok := any(NewMiddleware(opts...)).(T)
	if !ok {
		return mws
	}
	out := make([]T, 0, len(mws)+1)
	out = append(out, traced)
	return append(out, mws...)
}
