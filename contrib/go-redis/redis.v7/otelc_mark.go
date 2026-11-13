// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package redis

import (
	"context"
	"runtime"
	"slices"
	"sync"
	"weak"

	"github.com/go-redis/redis/v7"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// otelcEnabled is a variable so tests can turn the otelc behavior on.
var otelcEnabled = instrumentation.OtelcEnabled

type traceMarkKey struct{}

// traceMark records the hook that traces a command or a pipeline.
type traceMark struct {
	hook *datadogHook
	cmd  redis.Cmder
	cmds []redis.Cmder
}

func (m *traceMark) has(cmd redis.Cmder) bool {
	return m.cmd == cmd || slices.Contains(m.cmds, cmd)
}

// markOrSkip, in an otelc build, reports whether h must not trace cmd. If h
// traces it, it returns ctx marked with h. Plain builds skip all of it.
//
// The otelc rules wrap a Ring or Cluster and also the clients that its
// NewClient option builds with redis.NewClient, so both hooks see the same
// command. The mark keeps the outer span only.
func markOrSkip(ctx context.Context, h *datadogHook, cmd redis.Cmder) (context.Context, bool) {
	if !otelcEnabled() {
		return ctx, false
	}
	return mark(ctx, &traceMark{hook: h, cmd: cmd}, cmd)
}

// markOrSkipPipeline is markOrSkip for a pipeline. A Ring or Cluster sends
// each node a part of its pipeline, so the first command identifies it.
func markOrSkipPipeline(ctx context.Context, h *datadogHook, cmds []redis.Cmder) (context.Context, bool) {
	if !otelcEnabled() || len(cmds) == 0 {
		return ctx, false
	}
	return mark(ctx, &traceMark{hook: h, cmds: cmds}, cmds[0])
}

func mark(ctx context.Context, m *traceMark, first redis.Cmder) (context.Context, bool) {
	if m.hook.replaced.Load() {
		return ctx, true
	}
	if prev, ok := ctx.Value(traceMarkKey{}).(*traceMark); ok && prev.hook != m.hook && prev.has(first) {
		return ctx, true
	}
	return context.WithValue(ctx, traceMarkKey{}, m), false
}

// hooks maps a weak pointer to each client wrapped in an otelc build to its
// latest hook.
var hooks sync.Map

// replaceHook, in an otelc build, makes h the only hook of this package that
// traces client. The otelc rules wrap a client where it is built, so a later
// WrapClient call comes from the application, and its options apply.
func replaceHook(client redis.UniversalClient, h *datadogHook) {
	if !otelcEnabled() {
		return
	}
	// weak.Make needs a pointer to a concrete type, so each client type has its
	// own case. They are the types that the otelc rules build: a rule for
	// another client type must add it here.
	switch c := client.(type) {
	case *redis.Client:
		swapHook(c, h)
	case *redis.ClusterClient:
		swapHook(c, h)
	case *redis.Ring:
		swapHook(c, h)
	}
}

func swapHook[T any](client *T, h *datadogHook) {
	key := weak.Make(client)
	prev, loaded := hooks.Swap(key, h)
	if !loaded {
		runtime.AddCleanup(client, func(key weak.Pointer[T]) { hooks.Delete(key) }, key)
		return
	}
	prev.(*datadogHook).replaced.Store(true)
}

// tracedBy reports whether h traces the command in ctx: always in a plain
// build, and in an otelc build when markOrSkip marked ctx with h.
func tracedBy(ctx context.Context, h *datadogHook) bool {
	if !otelcEnabled() {
		return true
	}
	m, _ := ctx.Value(traceMarkKey{}).(*traceMark)
	return m != nil && m.hook == h
}
