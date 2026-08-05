// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTracestateSizeBounded covers the tracestate header: the header as a
// whole, and each non-dd vendor entry within it, must be size-bounded both
// when a header is first extracted and stored, and when it is later
// recomposed for injection.
func TestTracestateSizeBounded(t *testing.T) {
	t.Run("extract_bounds_oversized_vendor", func(t *testing.T) {
		t.Setenv(envPropagationStyle, "tracecontext")
		tr, err := newTracer()
		require.NoError(t, err)
		defer tr.Stop()

		oversizedVendor := strings.Repeat("A", 65000)
		headers := TextMapCarrier{
			traceparentHeader: "00-00000000000000000000000000000004-2222222222222222-01",
			tracestateHeader:  "vendor=" + oversizedVendor,
		}
		sctx, err := tr.Extract(headers)
		require.NoError(t, err)

		// Only the dd= list-member is size-checked today (tracestateDDMaxSize);
		// a non-dd vendor entry of any size currently passes through verbatim,
		// so the full attacker payload must not survive into the stored tag.
		stored := sctx.trace.propagatingTag(tracestateHeader)
		assert.NotContains(t, stored, oversizedVendor)
	})

	t.Run("extract_bounds_oversized_member_under_total_cap", func(t *testing.T) {
		t.Setenv(envPropagationStyle, "tracecontext")
		tr, err := newTracer()
		require.NoError(t, err)
		defer tr.Stop()

		// The header's total size is well under tracestateMaxSize (4096), so
		// only a per-member check -- not the whole-header check -- can catch
		// this. This must be filtered at parse/store time, not only when the
		// header is later recomposed for injection: other readers of the
		// stored tag (e.g. a SpanLink built from a terminated/restarted
		// extraction, or an inject that reuses a cached tracestate tag
		// verbatim) never go through composeTracestate at all.
		oversizedMember := "vendor=" + strings.Repeat("B", 520)
		raw := "dd=s:2;o:rum," + oversizedMember + ",othervendor=t61rcWkgMzE"
		require.Less(t, len(raw), tracestateMaxSize)
		require.Greater(t, len(oversizedMember), tracestateMemberMaxSize)

		headers := TextMapCarrier{
			traceparentHeader: "00-00000000000000000000000000000004-2222222222222222-01",
			tracestateHeader:  raw,
		}
		sctx, err := tr.Extract(headers)
		require.NoError(t, err)

		stored := sctx.trace.propagatingTag(tracestateHeader)
		assert.NotContains(t, stored, oversizedMember)
		assert.Contains(t, stored, "dd=s:2;o:rum")
		assert.Contains(t, stored, "othervendor=t61rcWkgMzE")
	})

	t.Run("extract_keeps_valid_small_tracestate", func(t *testing.T) {
		t.Setenv(envPropagationStyle, "tracecontext")
		tr, err := newTracer()
		require.NoError(t, err)
		defer tr.Stop()

		raw := "dd=s:2;o:rum,othervendor=t61rcWkgMzE"
		headers := TextMapCarrier{
			traceparentHeader: "00-00000000000000000000000000000004-2222222222222222-01",
			tracestateHeader:  raw,
		}
		sctx, err := tr.Extract(headers)
		require.NoError(t, err)
		assert.Equal(t, raw, sctx.trace.propagatingTag(tracestateHeader))
	})

	t.Run("inject_bounds_oversized_member", func(t *testing.T) {
		t.Setenv(envPropagationStyle, "tracecontext")
		tr, err := newTracer()
		require.NoError(t, err)
		defer tr.Stop()

		oversizedVendor := strings.Repeat("A", 65000)
		ctx := &SpanContext{traceID: traceIDFrom64Bits(1), spanID: 1, isRemote: true}
		setPropagatingTag(ctx, tracestateHeader, "vendor="+oversizedVendor)

		out := TextMapCarrier{}
		require.NoError(t, tr.Inject(ctx, out))
		assert.NotContains(t, out[tracestateHeader], oversizedVendor)
	})

	t.Run("inject_bounds_total_recomposed_size", func(t *testing.T) {
		t.Setenv(envPropagationStyle, "tracecontext")
		tr, err := newTracer()
		require.NoError(t, err)
		defer tr.Stop()

		// 8 vendor members, each within tracestateMemberMaxSize (512) on its
		// own, joined into a tracestate that is itself within
		// tracestateMaxSize (4096) -- i.e. exactly what parseTracestate would
		// have accepted and stored on extraction.
		var members []string
		for range 8 {
			members = append(members, "v="+strings.Repeat("A", 500))
		}
		oldState := strings.Join(members, ",")
		require.LessOrEqual(t, len(oldState), tracestateMaxSize)

		// composeTracestate always prepends its own dd= member on recompose.
		// A large origin inflates it enough that dd= + the vendor members
		// above exceeds tracestateMaxSize, even though neither the dd= member
		// nor any single vendor member is oversized on its own -- only the
		// recomposed total is.
		ctx := &SpanContext{traceID: traceIDFrom64Bits(1), spanID: 1, isRemote: true}
		ctx.origin = strings.Repeat("x", 100)
		setPropagatingTag(ctx, tracestateHeader, oldState)

		out := TextMapCarrier{}
		require.NoError(t, tr.Inject(ctx, out))
		assert.LessOrEqual(t, len(out[tracestateHeader]), tracestateMaxSize)
		// dd= is never dropped to make room; excess vendor members are.
		assert.True(t, strings.HasPrefix(out[tracestateHeader], "dd=s:"))
	})
}
