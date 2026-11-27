// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package glsunfinished

import (
	"context"
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/opentelemetry"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// glsWoven reports whether this build has the GLS woven in, by either
// Orchestrion or otelc.
var glsWoven = built.WithOrchestrion || otelc.Enabled()

func parentID(s *tracer.Span) uint64 {
	return s.AsMap()[ext.MapSpanParentID].(uint64)
}

func traceID(s *tracer.Span) uint64 {
	return s.AsMap()[ext.MapSpanTraceID].(uint64)
}

func TestUnfinishedSpanOnGLS(t *testing.T) {
	if !glsWoven {
		t.Skip("the GLS only exists in woven builds")
	}

	provider := opentelemetry.NewTracerProvider(tracer.WithLogStartup(false))
	defer provider.Shutdown()
	otel := provider.Tracer("gls-unfinished")

	// Control: a finished span leaves the GLS, so the later span is a root.
	t.Run("datadog/finished", func(t *testing.T) {
		done, _ := tracer.StartSpanFromContext(context.Background(), "done")
		done.Finish()

		later, _ := tracer.StartSpanFromContext(context.Background(), "later")
		defer later.Finish()

		require.Zero(t, parentID(later), "a finished span must not become a parent")
	})

	// The returned context is dropped, as moby does. The open span is on the
	// GLS only.
	t.Run("datadog/unfinished", func(t *testing.T) {
		open, _ := tracer.StartSpanFromContext(context.Background(), "open")
		defer open.Finish()

		later, _ := tracer.StartSpanFromContext(context.Background(), "later")
		defer later.Finish()

		require.Equal(t, open.Context().SpanID(), parentID(later), "the open span is the parent")
		require.Equal(t, traceID(open), traceID(later))
	})

	// An enclosing span that finishes on the same goroutine removes the
	// unfinished span above it (contextStack.PopScope), so the leak is bounded
	// by the enclosing scope.
	t.Run("datadog/enclosing-finish-drops-unfinished", func(t *testing.T) {
		root, ctx := tracer.StartSpanFromContext(context.Background(), "root")
		open, _ := tracer.StartSpanFromContext(ctx, "open")
		defer open.Finish()
		root.Finish()

		later, _ := tracer.StartSpanFromContext(context.Background(), "later")
		defer later.Finish()

		require.Zero(t, parentID(later), "the enclosing finish must drop the unfinished span from the GLS")
	})

	t.Run("opentelemetry/unfinished", func(t *testing.T) {
		ctx, openOTel := otel.Start(context.Background(), "open")
		defer openOTel.End()
		open, ok := tracer.SpanFromContext(ctx)
		require.True(t, ok)

		ctx, laterOTel := otel.Start(context.Background(), "later")
		defer laterOTel.End()
		later, ok := tracer.SpanFromContext(ctx)
		require.True(t, ok)

		require.Equal(t, open.Context().SpanID(), parentID(later), "the open span is the parent")
		require.Equal(t, traceID(open), traceID(later))
	})

	// Datadog and OpenTelemetry spans share one GLS stack.
	t.Run("datadog-open/opentelemetry-later", func(t *testing.T) {
		open, _ := tracer.StartSpanFromContext(context.Background(), "open")
		defer open.Finish()

		ctx, laterOTel := otel.Start(context.Background(), "later")
		defer laterOTel.End()
		later, ok := tracer.SpanFromContext(ctx)
		require.True(t, ok)

		require.Equal(t, open.Context().SpanID(), parentID(later), "the open span is the parent")
	})
}
