// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOTBaggageEnforcesItemAndByteLimits covers the ot-baggage-* prefix path
// on both extractors and the injector: unlike the W3C "baggage" header, this
// path currently has no item-count or byte-size cap.
func TestOTBaggageEnforcesItemAndByteLimits(t *testing.T) {
	t.Run("datadog_extractor_caps_items", func(t *testing.T) {
		t.Setenv(envPropagationStyle, "datadog")
		tr, err := newTracer()
		require.NoError(t, err)
		defer tr.Stop()

		headers := TextMapCarrier{
			DefaultTraceIDHeader:  "4",
			DefaultParentIDHeader: "1",
		}
		for i := range 100 {
			headers[fmt.Sprintf(DefaultBaggageHeaderPrefix+"k%d", i)] = "x"
		}
		sctx, err := tr.Extract(headers)
		require.NoError(t, err)

		got := map[string]string{}
		sctx.ForeachBaggageItem(func(k, v string) bool {
			got[k] = v
			return true
		})
		assert.LessOrEqual(t, len(got), baggageMaxItems)
	})

	t.Run("w3c_extractor_caps_items", func(t *testing.T) {
		t.Setenv(envPropagationStyle, "tracecontext")
		tr, err := newTracer()
		require.NoError(t, err)
		defer tr.Stop()

		headers := TextMapCarrier{
			traceparentHeader: "00-00000000000000000000000000000004-2222222222222222-01",
		}
		for i := range 100 {
			headers[fmt.Sprintf(DefaultBaggageHeaderPrefix+"k%d", i)] = "x"
		}
		sctx, err := tr.Extract(headers)
		require.NoError(t, err)

		got := map[string]string{}
		sctx.ForeachBaggageItem(func(k, v string) bool {
			got[k] = v
			return true
		})
		assert.LessOrEqual(t, len(got), baggageMaxItems)
	})

	t.Run("extractor_caps_bytes", func(t *testing.T) {
		t.Setenv(envPropagationStyle, "datadog")
		tr, err := newTracer()
		require.NoError(t, err)
		defer tr.Stop()

		headers := TextMapCarrier{
			DefaultTraceIDHeader:  "4",
			DefaultParentIDHeader: "1",
		}
		// 40 items, well under baggageMaxItems, whose combined size exceeds
		// baggageMaxBytes -- isolates the byte cap from the item-count cap.
		for i := range 40 {
			headers[fmt.Sprintf(DefaultBaggageHeaderPrefix+"k%d", i)] = strings.Repeat("a", 300)
		}
		sctx, err := tr.Extract(headers)
		require.NoError(t, err)

		total := 0
		sctx.ForeachBaggageItem(func(k, v string) bool {
			total += len(k) + len(v)
			return true
		})
		assert.LessOrEqual(t, total, baggageMaxBytes)
	})

	t.Run("datadog_injector_caps_items", func(t *testing.T) {
		t.Setenv(envPropagationStyle, "datadog")
		tr, err := newTracer()
		require.NoError(t, err)
		defer tr.Stop()

		ctx := &SpanContext{traceID: traceIDFrom64Bits(1), spanID: 1}
		for i := range 100 {
			ctx.setBaggageItem(fmt.Sprintf("k%d", i), "x")
		}

		out := TextMapCarrier{}
		require.NoError(t, tr.Inject(ctx, out))

		count := 0
		for k := range out {
			if strings.HasPrefix(strings.ToLower(k), DefaultBaggageHeaderPrefix) {
				count++
			}
		}
		assert.LessOrEqual(t, count, baggageMaxItems)
	})

	t.Run("datadog_injector_caps_bytes_including_prefix", func(t *testing.T) {
		// A custom, longer-than-default BaggagePrefix widens every emitted
		// header name; the byte cap must account for it, not just the
		// encoded key+value, or the actual emitted bytes can exceed
		// baggageMaxBytes even though this check passes.
		const prefix = "a-fairly-long-custom-ot-baggage-prefix-"
		propagator := NewPropagator(&PropagatorConfig{BaggagePrefix: prefix})
		t.Setenv(envPropagationStyle, "datadog")
		tr, err := newTracer(WithPropagator(propagator))
		require.NoError(t, err)
		defer tr.Stop()

		root := tr.StartSpan("web.request")
		for i := range baggageMaxItems {
			root.SetBaggageItem(fmt.Sprintf("k%d", i), strings.Repeat("a", 120))
		}
		ctx := root.Context()

		out := TextMapCarrier{}
		require.NoError(t, tr.Inject(ctx, out))

		total := 0
		for k, v := range out {
			if after, ok := strings.CutPrefix(k, prefix); ok {
				total += len(prefix) + len(after) + len(v)
			}
		}
		assert.LessOrEqual(t, total, baggageMaxBytes)
	})

	t.Run("legacy_extractor_warns_once_on_item_limit", func(t *testing.T) {
		// addOTBaggageItem is what the ot-baggage-* extractors call per
		// header; warned must flip on past the limit and stay on, so a
		// carrier with thousands of headers past baggageMaxItems logs once
		// instead of once per header.
		var baggage map[string]string
		var warned bool
		for i := range baggageMaxItems {
			baggage, _, warned = addOTBaggageItem(baggage, 0, warned, fmt.Sprintf("k%d", i), "v")
			assert.False(t, warned)
		}

		before := len(baggage)
		baggage, _, warned = addOTBaggageItem(baggage, 0, warned, "over-limit", "v")
		assert.True(t, warned)
		assert.Equal(t, before, len(baggage), "item past the limit must be dropped")

		_, _, warnedAgain := addOTBaggageItem(baggage, 0, warned, "still-over-limit", "v")
		assert.True(t, warnedAgain)
	})

	t.Run("baggage_header_still_capped", func(t *testing.T) {
		// Contrast check: the W3C "baggage" header path already enforces
		// these limits. Only the legacy ot-baggage-* prefix path is affected.
		t.Setenv(envPropagationStyle, "baggage")
		tr, err := newTracer()
		require.NoError(t, err)
		defer tr.Stop()

		var sb strings.Builder
		for i := range 100 {
			if i > 0 {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, "k%d=x", i)
		}
		sctx, err := tr.Extract(TextMapCarrier{DefaultBaggageHeader: sb.String()})
		require.NoError(t, err)

		got := map[string]string{}
		sctx.ForeachBaggageItem(func(k, v string) bool {
			got[k] = v
			return true
		})
		assert.LessOrEqual(t, len(got), baggageMaxItems)
	})
}
