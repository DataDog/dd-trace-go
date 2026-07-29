// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A rate-0 re-sample must clear a previously derived local (rv, th) so a stale
// threshold is not injected, while inherited values are left untouched.
func TestSetOtelProbabilityRateZero(t *testing.T) {
	t.Run("clears locally-derived pair", func(t *testing.T) {
		tr := newTrace()
		tr.setOtelProbability(0xfff972474538efff, 0.1)
		rv, th, _ := tr.otelTracestate()
		require.NotNil(t, rv)
		require.NotNil(t, th)

		tr.setOtelProbability(0xfff972474538efff, 0)
		rv, th, _ = tr.otelTracestate()
		assert.Nil(t, rv)
		assert.Nil(t, th)
	})

	t.Run("preserves inherited pair", func(t *testing.T) {
		tr := newTrace()
		tr.setOtelUpstream(0x1234567890abcd, true, 0xe6666666666668, true, "")
		tr.setOtelProbability(1, 0)
		rv, th, _ := tr.otelTracestate()
		require.NotNil(t, rv)
		require.NotNil(t, th)
		assert.Equal(t, uint64(0x1234567890abcd), *rv)
	})
}

// Golden vectors shared across dd-trace-* SDKs (see the OTel th/rv RFC). These
// pin the derivation so a given (trace ID, rate) yields an identical (rv, th)
// in every tracer.
func TestDeriveOtelGoldenVectors(t *testing.T) {
	// rate 0.1 -> th, independent of the trace ID.
	assert.Equal(t, uint64(0xe6666666666668), deriveOtelTH(0.1))
	// rate 1.0 keeps everything -> th 0.
	assert.Equal(t, uint64(0), deriveOtelTH(1.0))

	// W3C spec example trace ID low64, rate 0.1 -> DD would drop (rv < th).
	rvDrop := deriveOtelRV(0xa3ce929d0e0e4736)
	assert.Equal(t, uint64(0x02724cbf2e1fcf), rvDrop)
	assert.Less(t, rvDrop, deriveOtelTH(0.1))

	// Trace ID low64 that DD keeps at rate 0.1 -> rv >= th.
	rvKeep := deriveOtelRV(0xfff972474538efff)
	assert.Equal(t, uint64(0xef284ace7a91e1), rvKeep)
	assert.GreaterOrEqual(t, rvKeep, deriveOtelTH(0.1))
}

// Extremely small rates must not wrap to th:0 (keep-all); they clamp to the
// 56-bit maximum threshold instead.
func TestDeriveOtelTHClampsTinyRates(t *testing.T) {
	maxTH := uint64(1)<<56 - 1
	for _, rate := range []float64{math.Ldexp(1, -56), math.Ldexp(1, -54), 1e-18} {
		th := deriveOtelTH(rate)
		assert.Equal(t, maxTH, th, "rate %g should clamp to 56-bit max, got %#x", rate, th)
	}
	// A representable small rate is unaffected by the clamp.
	assert.Less(t, deriveOtelTH(1e-6), maxTH)
}

// The derived (rv, th) pair must reproduce DD's native keep/drop decision.
func TestDeriveOtelMatchesSampledByRate(t *testing.T) {
	rates := []float64{0.01, 0.1, 0.25, 0.5, 0.9, 0.99}
	for _, rate := range rates {
		th := deriveOtelTH(rate)
		for tid := uint64(1); tid < 5000; tid++ {
			ddKeep := sampledByRate(tid, rate)
			otelKeep := deriveOtelRV(tid) >= th
			if ddKeep != otelKeep {
				t.Fatalf("mismatch tid=%d rate=%v: dd=%v otel=%v", tid, rate, ddKeep, otelKeep)
			}
		}
	}
}

func TestFormatOtelValue(t *testing.T) {
	format := func(rv, th *uint64) string {
		var b strings.Builder
		appendOtelValue(&b, rv, th, "")
		return b.String()
	}
	ptr := func(v uint64) *uint64 { return &v }
	// rv fixed at 14 digits; th trailing zeros trimmed.
	assert.Equal(t, "rv:ef284ace7a91e1;th:e6666666666668", format(ptr(0xef284ace7a91e1), ptr(0xe6666666666668)))
	// th of 0 renders as a single "0".
	assert.Equal(t, "rv:00000000000001;th:0", format(ptr(1), ptr(0)))
	// rv-only (inherited rv, erased th).
	assert.Equal(t, "rv:0000000000000a", format(ptr(0xa), nil))
	// th-only (inherited OTel default-sampling decision).
	assert.Equal(t, "th:e6666666666668", format(nil, ptr(0xe6666666666668)))

	// Inherited unknown sub-keys are appended after rv/th (and stand alone when
	// rv/th are absent).
	var b strings.Builder
	appendOtelValue(&b, ptr(0xa), ptr(0xe6666666666668), "foo:1;bar:2")
	assert.Equal(t, "rv:0000000000000a;th:e6666666666668;foo:1;bar:2", b.String())
	b.Reset()
	appendOtelValue(&b, nil, nil, "foo:1")
	assert.Equal(t, "foo:1", b.String())
}

func TestParseOtelTracestate(t *testing.T) {
	rv, rvOK, th, thOK, unknown := parseOtelTracestate("rv:ef284ace7a91e1;th:e6666666666668")
	assert.True(t, rvOK)
	assert.True(t, thOK)
	assert.Equal(t, uint64(0xef284ace7a91e1), rv)
	assert.Equal(t, uint64(0xe6666666666668), th)
	assert.Empty(t, unknown)

	// th trailing zeros are restored on parse (round-trips with append).
	_, _, th, thOK, _ = parseOtelTracestate("th:e6666666666668")
	assert.True(t, thOK)
	assert.Equal(t, uint64(0xe6666666666668), th)

	// "th:0" is a valid zero threshold.
	_, _, th, thOK, _ = parseOtelTracestate("rv:00000000000001;th:0")
	assert.True(t, thOK)
	assert.Equal(t, uint64(0), th)

	// Sub-keys other than rv/th are captured verbatim, in arrival order, for
	// forwarding.
	_, _, _, _, unknown = parseOtelTracestate("rv:ef284ace7a91e1;foo:1;th:e6666666666668;bar:2")
	assert.Equal(t, "foo:1;bar:2", unknown)
}

func TestParseOtelTracestateMalformed(t *testing.T) {
	// rv must be exactly 14 hex digits.
	_, rvOK, _, _, _ := parseOtelTracestate("rv:abc")
	assert.False(t, rvOK)
	_, rvOK, _, _, _ = parseOtelTracestate("rv:ef284ace7a91e1ff")
	assert.False(t, rvOK)
	// non-hex is rejected.
	_, _, _, thOK, _ := parseOtelTracestate("th:zzzz")
	assert.False(t, thOK)
	// a bad rv doesn't poison a good th and vice versa.
	rv, rvOK, th, thOK, _ := parseOtelTracestate("rv:nothex;th:e6666666666668")
	assert.False(t, rvOK)
	assert.Zero(t, rv)
	assert.True(t, thOK)
	assert.Equal(t, uint64(0xe6666666666668), th)
}
