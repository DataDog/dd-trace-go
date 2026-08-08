// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// BenchmarkContextWithSpan guards the allocation cost of ContextWithSpan's
// call shapes: attaching a live span (the common StartSpanFromContext path),
// detaching a context that carries an ancestor snapshot (ContextWithSpan(ctx,
// nil), see TestStartSpanFromContextDetachRegression), and detaching a context
// that carries none — the tracer-disabled hot path that must stay
// allocation-free (see the "don't shadow a snapshot that isn't there" fix).
func BenchmarkContextWithSpan(b *testing.B) {
	_, _, _, stop, err := startTestTracer(b)
	require.NoError(b, err)
	defer stop()

	b.Run("live", func(b *testing.B) {
		s := StartSpan("op")
		defer s.Finish()
		ctx := context.Background()
		b.ReportAllocs()
		for b.Loop() {
			_ = ContextWithSpan(ctx, s)
		}
	})

	b.Run("detach", func(b *testing.B) {
		parent, pctx := StartSpanFromContext(context.Background(), "parent")
		defer parent.Finish()
		b.ReportAllocs()
		for b.Loop() {
			_ = ContextWithSpan(pctx, nil)
		}
	})

	b.Run("detach-no-snapshot", func(b *testing.B) {
		ctx := context.Background()
		b.ReportAllocs()
		for b.Loop() {
			_ = ContextWithSpan(ctx, nil)
		}
	})
}
