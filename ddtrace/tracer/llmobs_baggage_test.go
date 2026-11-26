// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
)

const (
	testLLMObsHexTraceID     = "6928d1a700000000abcdef0123456789"
	testLLMObsDecimalTraceID = "139780883686467862853683758018840586121"
)

// TestPropagatedLLMSpan covers the read rules of the dd-trace-py contract:
// carrier choice, trace ID fallbacks, filling from tags, and error types.
func TestPropagatedLLMSpan(t *testing.T) {
	type want struct {
		carrier, errType, parent, traceID, wireTraceID, mlApp, session, decision string
	}
	for _, tt := range []struct {
		name    string
		tags    map[string]string
		baggage map[string]string
		want    want
	}{
		{"nothing", nil, nil, want{carrier: llmobsCarrierNone, errType: llmobsErrMissingParentID}},
		{
			"tags only: the trace ID becomes canonical, the tag form is kept",
			map[string]string{keyPropagatedLLMObsParentID: "111", keyPropagatedLLMObsTraceID: testLLMObsDecimalTraceID, keyPropagatedLLMObsSamplingDecision: "1"},
			nil,
			want{carrier: llmobsCarrierTags, parent: "111", traceID: testLLMObsHexTraceID, wireTraceID: testLLMObsDecimalTraceID, decision: "1"},
		},
		{
			"baggage only",
			nil,
			map[string]string{baggageKeyLLMObsParentID: "222", baggageKeyLLMObsTraceID: testLLMObsHexTraceID, baggageKeyLLMObsSessionID: "s", baggageKeyLLMObsSamplingDecision: "0"},
			want{carrier: llmobsCarrierBaggage, parent: "222", traceID: testLLMObsHexTraceID, wireTraceID: testLLMObsDecimalTraceID, session: "s", decision: "0"},
		},
		{
			"same parent: baggage wins, missing fields come from the tags",
			map[string]string{keyPropagatedLLMObsParentID: "222", keyPropagatedLLMObsTraceID: testLLMObsDecimalTraceID, keyPropagatedLLMObsMLAPP: "app-tags", keyPropagatedLLMObsSessionID: "s-tags"},
			map[string]string{baggageKeyLLMObsParentID: "222", baggageKeyLLMObsTraceID: testLLMObsHexTraceID, baggageKeyLLMObsMLApp: "app-baggage"},
			want{carrier: llmobsCarrierBaggage, parent: "222", traceID: testLLMObsHexTraceID, wireTraceID: testLLMObsDecimalTraceID, mlApp: "app-baggage", session: "s-tags"},
		},
		{
			"different tag parent: stale baggage, no field from baggage",
			map[string]string{keyPropagatedLLMObsParentID: "111", keyPropagatedLLMObsTraceID: testLLMObsDecimalTraceID, keyPropagatedLLMObsMLAPP: "app-b"},
			map[string]string{baggageKeyLLMObsParentID: "222", baggageKeyLLMObsTraceID: "ab", baggageKeyLLMObsMLApp: "app-a", baggageKeyLLMObsSessionID: "s-a"},
			want{carrier: llmobsCarrierStaleBaggage, parent: "111", traceID: testLLMObsHexTraceID, wireTraceID: testLLMObsDecimalTraceID, mlApp: "app-b"},
		},
		{
			"root baggage parent with a real tag parent: stale baggage",
			map[string]string{keyPropagatedLLMObsParentID: "111", keyPropagatedLLMObsTraceID: testLLMObsDecimalTraceID},
			map[string]string{baggageKeyLLMObsParentID: "undefined"},
			want{carrier: llmobsCarrierStaleBaggage, parent: "111", traceID: testLLMObsHexTraceID, wireTraceID: testLLMObsDecimalTraceID},
		},
		{
			"root tag parent does not make baggage stale",
			map[string]string{keyPropagatedLLMObsParentID: "undefined"},
			map[string]string{baggageKeyLLMObsParentID: "222", baggageKeyLLMObsTraceID: "12345"},
			want{carrier: llmobsCarrierBaggage, parent: "222", traceID: "12345", wireTraceID: "12345"},
		},
		{
			"root parent only",
			nil,
			map[string]string{baggageKeyLLMObsParentID: "undefined"},
			want{carrier: llmobsCarrierBaggage, errType: llmobsErrMissingParentID},
		},
		{
			"non-numeric parent",
			nil,
			map[string]string{baggageKeyLLMObsParentID: "abc", baggageKeyLLMObsTraceID: testLLMObsHexTraceID},
			want{carrier: llmobsCarrierBaggage, errType: llmobsErrInvalidParentID},
		},
		{
			"non-canonical baggage trace ID falls back to the matching tag",
			map[string]string{keyPropagatedLLMObsParentID: "222", keyPropagatedLLMObsTraceID: testLLMObsDecimalTraceID},
			map[string]string{baggageKeyLLMObsParentID: "222", baggageKeyLLMObsTraceID: strings.ToUpper(testLLMObsHexTraceID)},
			want{carrier: llmobsCarrierBaggage, parent: "222", traceID: testLLMObsHexTraceID, wireTraceID: testLLMObsDecimalTraceID},
		},
		{
			"no usable trace ID falls back to the APM trace ID",
			nil,
			map[string]string{baggageKeyLLMObsParentID: "222", baggageKeyLLMObsTraceID: "0123"},
			want{carrier: llmobsCarrierBaggage, errType: llmobsErrMissingTraceID, parent: "222", traceID: "42", wireTraceID: "42"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sc := &SpanContext{trace: newTrace()}
			sc.traceID.SetLower(42)
			for k, v := range tt.tags {
				sc.trace.setPropagatingTag(k, v)
			}
			for k, v := range tt.baggage {
				sc.setBaggageItem(k, v)
			}

			p, carrier, errType := propagatedLLMSpan(sc)
			assert.Equal(t, tt.want.carrier, carrier)
			assert.Equal(t, tt.want.errType, errType)
			if tt.want.parent == "" {
				assert.Nil(t, p)
				return
			}
			require.NotNil(t, p)
			got := want{carrier, errType, p.SpanID, p.TraceID, p.WireTraceID, p.MLApp, p.SessionID, p.SamplingDecision}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLLMObsBaggageInject(t *testing.T) {
	defer illmobs.SetEnabledForTesting(true)()
	inject := func(sc *SpanContext, carrier TextMapCarrier) TextMapCarrier {
		require.NoError(t, (&propagatorBaggage{}).Inject(sc, carrier))
		return carrier
	}

	t.Run("owned keys go first, sampling is forwarded, inbound copies are replaced", func(t *testing.T) {
		sc := &SpanContext{trace: newTrace()}
		sc.setBaggageItem("tenant", "acme")
		sc.setBaggageItem(baggageKeyLLMObsParentID, "1")
		sc.setBaggageItem(baggageKeyLLMObsSamplingDecision, "1")
		sc.setLLMObsPropagated(&illmobs.PropagatedLLMSpan{SpanID: "555", TraceID: testLLMObsHexTraceID, SamplingDecision: "0"}, true)

		assert.Equal(t, TextMapCarrier{
			"baggage": "llmobs.parent_id=555,llmobs.trace_id=" + testLLMObsHexTraceID + ",llmobs.sampling_decision=0,tenant=acme",
		}, inject(sc, TextMapCarrier{}))
	})

	t.Run("without an LLMObs context no owned key is forwarded", func(t *testing.T) {
		sc := &SpanContext{trace: newTrace()}
		sc.setBaggageItem("tenant", "acme")
		sc.setBaggageItem(baggageKeyLLMObsParentID, "undefined")
		sc.setBaggageItem(baggageKeyLLMObsSampleRate, "0.5")

		assert.Equal(t, TextMapCarrier{"baggage": "tenant=acme"}, inject(sc, TextMapCarrier{}))
	})

	t.Run("the local root of an inbound request carries the inbound context", func(t *testing.T) {
		// A proxy that injects its server span directly, with no child span and no
		// context.Context to pass the LLMObs context on.
		tr, err := newTracer()
		require.NoError(t, err)
		defer tr.Stop()
		extracted, err := tr.Extract(TextMapCarrier{
			"baggage": "llmobs.parent_id=555,llmobs.trace_id=" + testLLMObsHexTraceID + ",tenant=acme",
		})
		require.NoError(t, err)
		root := tr.StartSpan("server", ChildOf(extracted))
		defer root.Finish()

		out := TextMapCarrier{}
		require.NoError(t, tr.Inject(root.Context(), out))
		assert.Equal(t, "llmobs.parent_id=555,llmobs.trace_id="+testLLMObsHexTraceID+",tenant=acme", out["baggage"])
		assert.Contains(t, out["x-datadog-tags"], "_dd.p.llmobs_parent_id=555")
	})

	t.Run("repeated HTTP baggage lines are all merged", func(t *testing.T) {
		sc := &SpanContext{trace: newTrace()}
		sc.setLLMObsPropagated(&illmobs.PropagatedLLMSpan{SpanID: "555", TraceID: testLLMObsHexTraceID}, true)
		carrier := HTTPHeadersCarrier{"Baggage": {"tenant=acme", "region=us"}}

		require.NoError(t, (&propagatorBaggage{}).Inject(sc, carrier))
		assert.Equal(t, []string{"llmobs.parent_id=555,llmobs.trace_id=" + testLLMObsHexTraceID + ",tenant=acme,region=us"}, carrier["Baggage"])
	})

	t.Run("a reused pointer carrier drops stale llmobs items", func(t *testing.T) {
		withLLMObs := &SpanContext{trace: newTrace()}
		withLLMObs.setLLMObsPropagated(&illmobs.PropagatedLLMSpan{SpanID: "555", TraceID: testLLMObsHexTraceID}, true)
		carrier := &TextMapCarrier{}
		require.NoError(t, (&propagatorBaggage{}).Inject(withLLMObs, carrier))
		require.Contains(t, (*carrier)["baggage"], "llmobs.parent_id=555")

		require.NoError(t, (&propagatorBaggage{}).Inject(&SpanContext{trace: newTrace()}, carrier))
		assert.Empty(t, *carrier)

		*carrier = TextMapCarrier{"Baggage": "tenant=acme"}
		require.NoError(t, (&propagatorBaggage{}).Inject(withLLMObs, carrier))
		assert.Equal(t, TextMapCarrier{
			"baggage": "llmobs.parent_id=555,llmobs.trace_id=" + testLLMObsHexTraceID + ",tenant=acme",
		}, *carrier)
	})

	t.Run("a caller header under Baggage is merged into one key", func(t *testing.T) {
		sc := &SpanContext{trace: newTrace()}
		sc.setLLMObsPropagated(&illmobs.PropagatedLLMSpan{SpanID: "555", TraceID: testLLMObsHexTraceID}, true)

		assert.Equal(t, TextMapCarrier{
			"baggage": "llmobs.parent_id=555,llmobs.trace_id=" + testLLMObsHexTraceID + ",tenant=acme;p=1",
		}, inject(sc, TextMapCarrier{"Baggage": "tenant=acme;p=1,llmobs.parent_id=9"}))
	})
}

func TestBaggageInjectWithoutLLMObsReplacesCallerHeader(t *testing.T) {
	// With LLMObs disabled, the injector behaves as before: the context's items
	// replace a header the caller set, and llmobs.* items are ordinary baggage.
	defer illmobs.SetEnabledForTesting(false)()
	sc := &SpanContext{trace: newTrace()}
	sc.setBaggageItem(baggageKeyLLMObsParentID, "1")
	carrier := TextMapCarrier{"baggage": "tenant=a"}

	require.NoError(t, (&propagatorBaggage{}).Inject(sc, carrier))

	assert.Equal(t, TextMapCarrier{"baggage": "llmobs.parent_id=1"}, carrier)
}

func TestLLMObsBaggageItemValues(t *testing.T) {
	item := func(dc illmobs.DistributedContext, key string) string {
		for _, it := range llmobsBaggageItems(dc) {
			if it[0] == key {
				return it[1]
			}
		}
		return ""
	}
	for _, tt := range []struct {
		name, agentName, session, wantAgentName, wantSession string
	}{
		{"ascii name capped at 128 encoded bytes", strings.Repeat("a", 600), "s", strings.Repeat("a", 128), "s"},
		// Each character percent-encodes to 9 bytes, so 14 fit in 128, cut on a rune boundary.
		{"multi-byte name capped on a rune boundary", strings.Repeat("研", 600), "s", strings.Repeat("研", 14), "s"},
		{"blank values are not sent", " ", " ", "", ""},
		{"an oversized value is not sent", "a", strings.Repeat("s", 600), "a", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dc := illmobs.DistributedContext{ParentID: "1", SessionID: tt.session, ParentAgentSpanID: "2", ParentAgentName: tt.agentName}
			assert.Equal(t, tt.wantAgentName, item(dc, baggageKeyLLMObsParentAgentName))
			assert.Equal(t, tt.wantSession, item(dc, baggageKeyLLMObsSessionID))
			assert.Equal(t, "2", item(dc, baggageKeyLLMObsParentAgentSpanID), "items after a dropped one are still sent")
		})
	}
	// Other tracers decode baggage with W3C percent-decoding, which reads "+" as a plus sign.
	assert.Equal(t, "Research%20Agent", encodeValue("Research Agent"))
}

func TestLLMObsDisabledForwardsInboundContextUnchanged(t *testing.T) {
	// A service without LLMObs must not read, rewrite, or add to LLMObs context:
	// the added _dd.p.llmobs_* tags could push x-datadog-tags over its budget.
	defer illmobs.SetEnabledForTesting(false)()
	tr, err := newTracer()
	require.NoError(t, err)
	defer tr.Stop()
	extracted, err := tr.Extract(TextMapCarrier{
		"baggage": "llmobs.parent_id=555,llmobs.trace_id=" + testLLMObsHexTraceID + ",tenant=acme",
	})
	require.NoError(t, err)
	root := tr.StartSpan("server", ChildOf(extracted))
	defer root.Finish()
	child := tr.StartSpan("client", ChildOf(root.Context()))
	defer child.Finish()

	_, propagated := root.Context().llmobsContext()
	assert.Nil(t, propagated)
	out := TextMapCarrier{}
	require.NoError(t, tr.Inject(child.Context(), out))
	assert.ElementsMatch(t,
		[]string{"llmobs.parent_id=555", "llmobs.trace_id=" + testLLMObsHexTraceID, "tenant=acme"},
		strings.Split(out["baggage"], ","))
	assert.NotContains(t, out["x-datadog-tags"], "llmobs")
}

func TestLLMObsInboundValuesCannotBreakTheTagsHeader(t *testing.T) {
	// ml_app and session_id can arrive in an inbound baggage header, which nothing
	// caps. An invalid or oversized one must not make the encoder drop the whole
	// x-datadog-tags header (and _dd.p.dm with it).
	defer illmobs.SetEnabledForTesting(true)()
	tr, err := newTracer()
	require.NoError(t, err)
	defer tr.Stop()
	for name, extra := range map[string]string{
		"oversized ml_app": ",llmobs.ml_app=" + strings.Repeat("a", 400),
	} {
		t.Run(name, func(t *testing.T) {
			extracted, err := tr.Extract(TextMapCarrier{
				"x-datadog-trace-id": "123", "x-datadog-parent-id": "456", "x-datadog-sampling-priority": "1",
				"baggage": "llmobs.parent_id=555,llmobs.trace_id=" + testLLMObsHexTraceID + extra,
			})
			require.NoError(t, err)
			root := tr.StartSpan("server", ChildOf(extracted))
			defer root.Finish()

			out := TextMapCarrier{}
			require.NoError(t, tr.Inject(root.Context(), out))
			tags := out["x-datadog-tags"]
			assert.Contains(t, tags, "_dd.p.llmobs_parent_id=555", "the header was not dropped")
			assert.NotContains(t, tags, "_dd.p.llmobs_ml_app")
			assert.NotContains(t, tags, "_dd.p.llmobs_sid")
		})
	}
}

func TestLLMObsExtractedContextInjectedDirectly(t *testing.T) {
	// A relay that injects an extracted context, with no local span, forwards it.
	defer illmobs.SetEnabledForTesting(true)()
	tr, err := newTracer()
	require.NoError(t, err)
	defer tr.Stop()
	extracted, err := tr.Extract(TextMapCarrier{
		"x-datadog-trace-id": "123", "x-datadog-parent-id": "456", "x-datadog-sampling-priority": "1",
		"x-datadog-tags": "_dd.p.llmobs_parent_id=555,_dd.p.llmobs_trace_id=" + testLLMObsDecimalTraceID,
	})
	require.NoError(t, err)
	out := TextMapCarrier{}
	require.NoError(t, tr.Inject(extracted, out))
	assert.Contains(t, out["x-datadog-tags"], "_dd.p.llmobs_parent_id=555")
	assert.Contains(t, out["x-datadog-tags"], "_dd.p.llmobs_trace_id="+testLLMObsDecimalTraceID)
	assert.Contains(t, out["baggage"], "llmobs.parent_id=555")
}

func TestSpanContextSizeClass(t *testing.T) {
	// Every span allocates a SpanContext. At 257 bytes it would move to the next
	// size class (288), so per-feature state belongs on the trace, not here.
	assert.LessOrEqual(t, unsafe.Sizeof(SpanContext{}), uintptr(256))
}

func TestLLMObsBaggageValuesRoundTrip(t *testing.T) {
	// "+" and "%" are written escaped, so the extractor, which decodes "+" as a
	// space, returns the value unchanged. Base64 session IDs contain "+".
	for _, v := range []string{"abc+def==", "app%41", "a b"} {
		carrier := TextMapCarrier{}
		sc := &SpanContext{trace: newTrace()}
		sc.setBaggageItem("v", v)
		require.NoError(t, (&propagatorBaggage{}).Inject(sc, carrier))
		extracted, err := (&propagatorBaggage{}).Extract(carrier)
		require.NoError(t, err)
		assert.Equal(t, v, extracted.baggageItem("v"), "header %q", carrier["baggage"])
	}
}

func TestLLMObsStaleOptionalTagsDoNotCountAgainstTheBudget(t *testing.T) {
	// A previous injection left a long session in the trace-wide tags. This request
	// has no session and a long ml_app, which fits the tracestate budget only once
	// the stale session is gone.
	defer illmobs.SetEnabledForTesting(true)()
	sc := &SpanContext{trace: newTrace()}
	sc.trace.setPropagatingTag(keyPropagatedLLMObsSessionID, strings.Repeat("s", 80))
	mlApp := strings.Repeat("a", 80)
	sc.setLLMObsPropagated(&illmobs.PropagatedLLMSpan{SpanID: "555", TraceID: "12345", WireTraceID: "12345", MLApp: mlApp}, true)

	setLLMObsTagsForInjection(sc)

	assert.Equal(t, mlApp, sc.trace.propagatingTag(keyPropagatedLLMObsMLAPP))
	assert.Empty(t, sc.trace.propagatingTag(keyPropagatedLLMObsSessionID))
}

func TestLLMObsPartialFlushForgetsFlushedSpans(t *testing.T) {
	// On a long-running trace, the span map must not keep every LLMObs span that
	// partial flushing already sent.
	t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
	t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "2")
	tr, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()
	root := tr.StartSpan("root")
	defer root.Finish()
	llmSpan := &illmobs.Span{}
	for range 4 {
		child := tr.StartSpan("child", ChildOf(root.Context()))
		child.Context().setLLMObsSpan(llmSpan)
		child.Finish()
	}

	s := root.Context().trace.llmobs.Load()
	require.NotNil(t, s)
	s.mu.RLock()
	defer s.mu.RUnlock()
	assert.Empty(t, s.spans)
}
