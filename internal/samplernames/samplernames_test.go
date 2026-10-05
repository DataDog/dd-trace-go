// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package samplernames

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSamplerDecisionMaker(t *testing.T) {
	testCases := []struct {
		name     string
		sampler  SamplerName
		expected string
	}{
		{
			name:     "Unknown",
			sampler:  Unknown,
			expected: "--1",
		},
		{
			name:     "Default",
			sampler:  Default,
			expected: "-0",
		},
		{
			name:     "AgentRate",
			sampler:  AgentRate,
			expected: "-1",
		},
		{
			name:     "RemoteRate",
			sampler:  RemoteRate,
			expected: "-2",
		},
		{
			name:     "RuleRate",
			sampler:  RuleRate,
			expected: "-3",
		},
		{
			name:     "Manual",
			sampler:  Manual,
			expected: "-4",
		},
		{
			name:     "AppSec",
			sampler:  AppSec,
			expected: "-5",
		},
		{
			name:     "RemoteUserRate",
			sampler:  RemoteUserRate,
			expected: "-6",
		},
		{
			name:     "SingleSpan",
			sampler:  SingleSpan,
			expected: "-8",
		},
		{
			name:     "RemoteUserRule",
			sampler:  RemoteUserRule,
			expected: "-11",
		},
		{
			name:     "RemoteDynamicRule",
			sampler:  RemoteDynamicRule,
			expected: "-12",
		},
		{
			name:     "Invalid (default to Unknown)",
			sampler:  SamplerName(99),
			expected: "--1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.sampler.DecisionMaker()
			assert.Equal(t, tc.expected, result)
		})
	}
}

func BenchmarkSamplerDecisionMaker(b *testing.B) {
	sampler := RuleRate
	oldSampleToDM := func(sampler SamplerName) string {
		return "-" + strconv.Itoa(int(sampler))
	}
	b.ResetTimer()
	b.Run("current", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = sampler.DecisionMaker()
		}
	})
	b.Run("old", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = oldSampleToDM(sampler)
		}
	})
}
