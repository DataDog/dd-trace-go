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

// TestTracestateSizeBounded covers the tracestate header: only the dd= list
// member is size-capped today. Non-Datadog vendor entries -- and the header
// as a whole -- are stored and re-emitted verbatim with no bound.
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
}
