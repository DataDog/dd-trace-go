// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

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
	format := func(rv, th uint64, thSet bool) string {
		var b strings.Builder
		appendOtelValue(&b, rv, th, thSet)
		return b.String()
	}
	// rv fixed at 14 digits; th trailing zeros trimmed.
	assert.Equal(t, "rv:ef284ace7a91e1;th:e6666666666668", format(0xef284ace7a91e1, 0xe6666666666668, true))
	// th of 0 renders as a single "0".
	assert.Equal(t, "rv:00000000000001;th:0", format(1, 0, true))
	// rv-only (inherited rv, erased th).
	assert.Equal(t, "rv:0000000000000a", format(0xa, 0, false))
}

func TestParseOtelTracestate(t *testing.T) {
	rv, rvOK, th, thOK := parseOtelTracestate("rv:ef284ace7a91e1;th:e6666666666668")
	assert.True(t, rvOK)
	assert.True(t, thOK)
	assert.Equal(t, uint64(0xef284ace7a91e1), rv)
	assert.Equal(t, uint64(0xe6666666666668), th)

	// th trailing zeros are restored on parse (round-trips with append).
	_, _, th, thOK = parseOtelTracestate("th:e6666666666668")
	assert.True(t, thOK)
	assert.Equal(t, uint64(0xe6666666666668), th)

	// "th:0" is a valid zero threshold.
	_, _, th, thOK = parseOtelTracestate("rv:00000000000001;th:0")
	assert.True(t, thOK)
	assert.Equal(t, uint64(0), th)
}

func TestParseOtelTracestateMalformed(t *testing.T) {
	// rv must be exactly 14 hex digits.
	_, rvOK, _, _ := parseOtelTracestate("rv:abc")
	assert.False(t, rvOK)
	_, rvOK, _, _ = parseOtelTracestate("rv:ef284ace7a91e1ff")
	assert.False(t, rvOK)
	// non-hex is rejected.
	_, _, _, thOK := parseOtelTracestate("th:zzzz")
	assert.False(t, thOK)
	// a bad rv doesn't poison a good th and vice versa.
	rv, rvOK, th, thOK := parseOtelTracestate("rv:nothex;th:e6666666666668")
	assert.False(t, rvOK)
	assert.Zero(t, rv)
	assert.True(t, thOK)
	assert.Equal(t, uint64(0xe6666666666668), th)
}
