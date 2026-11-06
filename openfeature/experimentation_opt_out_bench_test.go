// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"strconv"
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/require"
)

// BenchmarkBooleanEvaluationExclusion includes policy resolution and allocation
// traversal, so it measures call-site overhead that the helper benchmark misses.
func BenchmarkBooleanEvaluationExclusion(b *testing.B) {
	activation := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	for _, policy := range []struct {
		name   string
		mode   ExperimentationOptOutMode
		cutoff string
		want   bool
	}{
		{"disabled", ExperimentationOptOutDisabled, "", true},
		{"immediate_absent", ExperimentationOptOutImmediate, "", true},
		{"future_absent", ExperimentationOptOutFutureExperimentsOnly, "", true},
		{"immediate_active", ExperimentationOptOutImmediate, activation.Format(time.RFC3339Nano), false},
		{"future_active", ExperimentationOptOutFutureExperimentsOnly, activation.Format(time.RFC3339Nano), false},
		{"future_retained", ExperimentationOptOutFutureExperimentsOnly, activation.Add(time.Second).Format(time.RFC3339Nano), true},
	} {
		for _, allocationCount := range []int{1, 16} {
			for _, extraFields := range []int{0, 20} {
				name := policy.name + "/allocations=" + strconv.Itoa(allocationCount) + "/extra_fields=" + strconv.Itoa(extraFields)
				b.Run(name, func(b *testing.B) {
					provider := newDatadogProvider(ProviderConfig{ExperimentationOptOutMode: policy.mode})
					b.Cleanup(provider.Shutdown)
					config := createTestConfig()
					f := config.Flags["bool-flag"]
					template := f.Allocations[0]
					f.Allocations = make([]*allocation, allocationCount)
					for i := range allocationCount {
						a := *template
						a.Key = "allocation-" + strconv.Itoa(i)
						a.DoLog = new(true)
						a.RandomizationActivatedAt = &activation
						// Earlier allocations miss targeting, forcing full traversal.
						if i < allocationCount-1 {
							a.Rules = []*rule{{Conditions: []*condition{{Operator: operatorOneOf, Attribute: "country", Value: []string{"CA"}}}}}
						}
						f.Allocations[i] = &a
					}
					provider.updateConfiguration(config)
					ctx := context.Background()
					attrs := of.FlattenedContext{of.TargetingKey: "user-123", "country": "US"}
					if policy.cutoff != "" {
						attrs[experimentationOptOutAtAttribute] = policy.cutoff
					}
					for i := range extraFields {
						attrs["field-"+strconv.Itoa(i)] = i
					}
					result := provider.BooleanEvaluation(ctx, "bool-flag", false, attrs)
					require.Empty(b, result.ResolutionDetail().ErrorCode)
					require.Equal(b, policy.want, result.Value)
					b.ReportAllocs()
					for b.Loop() {
						_ = provider.BooleanEvaluation(ctx, "bool-flag", false, attrs)
					}
				})
			}
		}
	}
}
