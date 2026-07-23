// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"

	"github.com/stretchr/testify/assert"
)

// injectTracestate injects ctx with the W3C propagator and returns the
// resulting tracestate header.
func injectTracestate(t *testing.T, ctx *SpanContext) string {
	t.Helper()
	carrier := TextMapCarrier(map[string]string{})
	p := &propagatorW3c{}
	assert.NoError(t, p.Inject(ctx, carrier))
	return carrier[tracestateHeader]
}

// A genuine probability decision emits ot=rv:...;th:... as the second member,
// right after dd=.
func TestInjectEmitsOtelOnProbabilityDecision(t *testing.T) {
	const tid = uint64(0xfff972474538efff)
	ctx := &SpanContext{traceID: traceIDFrom64Bits(tid), spanID: 1}
	ctx.trace = newTrace()
	ctx.trace.setSamplingPriority(ext.PriorityAutoKeep, samplernames.AgentRate)
	ctx.trace.setOtelProbability(tid, 0.1)

	ts := injectTracestate(t, ctx)
	assert.Contains(t, ts, "ot=rv:ef284ace7a91e1;th:e6666666666668")
	// dd= stays first, ot= second.
	assert.True(t, len(ts) > 3 && ts[:3] == "dd=")
	assert.Regexp(t, `^dd=[^,]*,ot=rv:ef284ace7a91e1;th:e6666666666668$`, ts)
}

// End-to-end: a root span decided by the global sample rate emits ot= when
// injected. At rate 1.0 every trace is kept and th is 0.
func TestInjectEmitsOtelEndToEnd(t *testing.T) {
	t.Setenv("DD_TRACE_SAMPLE_RATE", "1.0")
	t.Setenv(envPropagationStyle, "tracecontext")
	tr, err := newTracer()
	assert.NoError(t, err)
	defer tr.Stop()

	span := tr.StartSpan("op")
	carrier := TextMapCarrier(map[string]string{})
	assert.NoError(t, tr.Inject(span.Context(), carrier))
	span.Finish()

	ts := carrier[tracestateHeader]
	assert.Regexp(t, `,ot=rv:[0-9a-f]{14};th:0$`, ts)
}

// A force-keep is not a probability decision: no th is emitted, and with no
// inherited rv there is no ot= member at all.
func TestInjectNoOtelOnForceKeep(t *testing.T) {
	const tid = uint64(0xfff972474538efff)
	ctx := &SpanContext{traceID: traceIDFrom64Bits(tid), spanID: 1}
	ctx.trace = newTrace()
	// Probability decision first, then a manual keep overrides it.
	ctx.trace.setOtelProbability(tid, 0.1)
	ctx.trace.forceSetSamplingPriority(ext.PriorityUserKeep, samplernames.Manual)

	ts := injectTracestate(t, ctx)
	assert.NotContains(t, ts, "ot=")
}

// An inbound ot= is parsed on extract and forwarded verbatim on inject, hoisted
// to the second member; other vendors and dd= are preserved.
func TestOtelInheritedForwardedUnchanged(t *testing.T) {
	t.Setenv(envPropagationStyle, "tracecontext")
	tr, err := newTracer()
	assert.NoError(t, err)
	defer tr.Stop()

	headers := TextMapCarrier(map[string]string{
		traceparentHeader: "00-4bf92f3577b34da6a3ce929d0e0e4736-2222222222222222-01",
		tracestateHeader:  "dd=s:1;t.dm:-4,ot=rv:ef284ace7a91e1;th:e6666666666668,othervendor=abc",
	})
	sctx, err := tr.Extract(headers)
	assert.NoError(t, err)

	rv, th, rvSet, thSet := sctx.trace.otelTracestate()
	assert.True(t, rvSet)
	assert.True(t, thSet)
	assert.Equal(t, uint64(0xef284ace7a91e1), rv)
	assert.Equal(t, uint64(0xe6666666666668), th)
	assert.True(t, sctx.trace.otInherited)

	ts := injectTracestate(t, sctx)
	assert.Contains(t, ts, "ot=rv:ef284ace7a91e1;th:e6666666666668")
	assert.Contains(t, ts, "othervendor=abc")
	// ot= appears exactly once (no duplicate from passthrough).
	assert.Equal(t, 1, strings.Count(ts, "ot=rv:"))
}

// A malformed ot= is treated as absent: no inherited values, trace not rejected,
// dd= and other vendors preserved.
func TestOtelMalformedTreatedAsAbsent(t *testing.T) {
	t.Setenv(envPropagationStyle, "tracecontext")
	tr, err := newTracer()
	assert.NoError(t, err)
	defer tr.Stop()

	headers := TextMapCarrier(map[string]string{
		traceparentHeader: "00-4bf92f3577b34da6a3ce929d0e0e4736-2222222222222222-01",
		tracestateHeader:  "dd=s:1;t.dm:-4,ot=rv:nothex;th:zzz,othervendor=abc",
	})
	sctx, err := tr.Extract(headers)
	assert.NoError(t, err)

	_, _, rvSet, thSet := sctx.trace.otelTracestate()
	assert.False(t, rvSet)
	assert.False(t, thSet)
	assert.False(t, sctx.trace.otInherited)

	ts := injectTracestate(t, sctx)
	assert.Contains(t, ts, "othervendor=abc")
	assert.NotContains(t, ts, "ot=")
}
