// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc compile-time instrumentation hooks for
// github.com/gomodule/redigo. It is not part of this module's public API: it
// exists only to be linked in by the rules in otelc.yaml.
//
// redis.Dial, redis.DialURL and redis.DialURLContext all funnel into
// redis.DialContext, so hooking that one definition covers every entry point
// the orchestrion aspects wrap at their call sites.
package otelc

import (
	"context"

	"github.com/gomodule/redigo/redis"
	"go.opentelemetry.io/otelc/pkg/hook"

	redigotrace "github.com/DataDog/dd-trace-go/contrib/gomodule/redigo/v2"
)

// dialResult carries the traced connection from the before hook to the after
// hook, which is the only place otelc lets us write the return values.
type dialResult struct {
	conn redis.Conn
	err  error
}

// BeforeDialContext substitutes redigotrace.DialContext for redis.DialContext.
//
// The contrib marks the dial it makes itself, which covers both directions of the
// same problem: the inner call this hook triggers is let through instead of
// recursing, and an application that called the contrib directly keeps the single
// wrapper it already has rather than getting a second one.
func BeforeDialContext(ictx hook.HookContext, ctx context.Context, network, address string, options ...redis.DialOption) {
	if redigotrace.TraceMarked(ctx) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	opts := make([]any, len(options))
	for i, opt := range options {
		opts[i] = opt
	}
	conn, err := redigotrace.DialContext(ctx, network, address, opts...)
	ictx.SetData(dialResult{conn: conn, err: err})
	ictx.SetSkipCall(true)
}

// AfterDialContext writes back what BeforeDialContext produced. It is a no-op
// when the original call was allowed to run.
func AfterDialContext(ictx hook.HookContext, _ redis.Conn, _ error) {
	res, ok := ictx.GetData().(dialResult)
	if !ok {
		return
	}
	// SetReturnVal discards nil values, and skipping the call left both return
	// values zeroed, so only the non-nil ones need writing.
	if res.conn != nil {
		ictx.SetReturnVal(0, res.conn)
	}
	if res.err != nil {
		ictx.SetReturnVal(1, res.err)
	}
}
