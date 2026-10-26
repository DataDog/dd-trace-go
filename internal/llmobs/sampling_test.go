// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSampledByRateMatchesOtherTracers(t *testing.T) {
	// Expected decisions computed with dd-trace-py's LLMObsSamplingRule.sample:
	// ((trace_id * 1111111111111111111) % 2**64) <= rate * (2**64 - 1).
	// Every SDK in a distributed trace must agree for the same trace ID.
	for _, tc := range []struct {
		traceID string
		rate    float64
		want    bool
	}{
		{"0000000000000001", 0.5, true},
		{"0000000000000002", 0.5, true},
		{"ab54a98ceb1f0ad2", 0.5, true},
		{"a3ce929d0e0e4736", 0.5, false},
		{"8000000000000000", 0.5, true},
		{"f2a74de452e6b438", 0.5, false},
		{"6513270e269e0d37", 0.5, true},
		{"0c5c7fd0a6a3a450", 0.5, false},
		{"d23f0824128b2f33", 0.5, true},
		{"1818e811892f902b", 0.5, true},
		{"0000000000000001", 0.1, true},
		{"0000000000000002", 0.1, false},
		{"ab54a98ceb1f0ad2", 0.1, false},
		{"6513270e269e0d37", 0.1, true},
		{"d23f0824128b2f33", 0.1, false},
	} {
		got := sampledByRate(lower64TraceID(tc.traceID), tc.rate)
		assert.Equal(t, tc.want, got, "trace ID %s at rate %v", tc.traceID, tc.rate)
	}
}

func TestSampledByRateBounds(t *testing.T) {
	for _, id := range []uint64{0, 1, 1 << 63, ^uint64(0)} {
		assert.True(t, sampledByRate(id, 1), "rate 1 keeps every trace")
		assert.False(t, sampledByRate(id, 0), "rate 0 drops every trace")
	}
}

func TestLower64TraceID(t *testing.T) {
	// 128-bit APM trace IDs are hex encoded; only the low 64 bits are hashed.
	assert.Equal(t, uint64(0xa3ce929d0e0e4736), lower64TraceID("6578a1b200000000a3ce929d0e0e4736"))
	assert.Equal(t, uint64(0xa3ce929d0e0e4736), lower64TraceID("a3ce929d0e0e4736"))
	assert.Equal(t, uint64(0x1), lower64TraceID("1"))
	assert.Equal(t, uint64(0), lower64TraceID("not-hex"))
}

func TestFormatSampleRate(t *testing.T) {
	for rate, want := range map[float64]string{
		1:         "1",
		0:         "0",
		0.5:       "0.5",
		0.25:      "0.25",
		0.1234567: "0.123457",
		0.0000001: "0",
	} {
		assert.Equal(t, want, formatSampleRate(rate), "rate %v", rate)
	}
}

func TestRootSamplingDecision(t *testing.T) {
	const traceID = "6578a1b200000000a3ce929d0e0e4736"
	assert.Equal(t, samplingDecision{rate: "1", decision: SamplingDecisionKeep}, rootSamplingDecision(traceID, 1))
	assert.Equal(t, samplingDecision{rate: "0", decision: SamplingDecisionDrop}, rootSamplingDecision(traceID, 0))
	assert.Equal(t, samplingDecision{rate: "0.5", decision: SamplingDecisionDrop}, rootSamplingDecision(traceID, 0.5))
}
