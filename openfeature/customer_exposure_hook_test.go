// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
)

func TestCustomerExposureHooksShareDecision(t *testing.T) {
	internalconfig.SetUseFreshConfig(true)
	t.Cleanup(func() { internalconfig.SetUseFreshConfig(false); internalconfig.CreateNew() })
	for _, send := range []bool{true, false} {
		t.Run(fmt.Sprint(send), func(t *testing.T) {
			t.Setenv("DD_FEATURE_FLAGS_EXPOSURES_DATADOG_LOGGING_ENABLED", fmt.Sprint(send))
			h := newOptOutHarness(t, ProviderConfig{})
			var first, second []ExposureEvaluation
			h.client.AddHooks(NewExposureHook(func(e ExposureEvaluation) { first = append(first, e) }))
			h.client.AddHooks(NewExposureHook(func(e ExposureEvaluation) {
				second = append(second, e)
				e.Details.FlagMetadata[metadataExposureCacheHitKey] = "customer edit"
			}))
			h.evaluate(t, "athlete", nil)
			h.evaluate(t, "athlete", nil)
			h.evaluate(t, "other-athlete", nil)
			for _, observations := range [][]ExposureEvaluation{first, second} {
				require.Len(t, observations, 3)
				assert.True(t, observations[0].IsExposure())
				assert.Equal(t, "athlete", observations[0].Context.TargetingKey())
				assert.Equal(t, uint32(42), observations[0].Details.FlagMetadata[metadataSplitSerialIDKey])
			}
			assert.True(t, first[0].ShouldSend())
			assert.False(t, first[0].IsCacheHit())
			assert.False(t, first[1].ShouldSend())
			assert.True(t, first[1].IsCacheHit())
			assert.True(t, first[2].ShouldSend())
			want := 0
			if send {
				want = 2
			}
			h.assertCounts(t, 3, want, 3)
		})
	}
}

func TestCustomerExposureHookChangesAndNonExposures(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{})
	var rows []ExposureEvaluation
	h.client.AddHooks(NewExposureHook(func(e ExposureEvaluation) { rows = append(rows, e) }))
	allocation := h.provider.configuration.Flags["experiment"].Allocations[0]
	for _, serial := range []*uint32{nil, new(uint32(0)), new(uint32(42)), new(uint32(0)), nil} {
		allocation.Splits[0].SerialID = serial
		h.evaluate(t, "athlete", nil)
		h.evaluate(t, "athlete", nil)
	}
	for i, row := range rows {
		assert.Equal(t, i%2 == 0, row.ShouldSend(), "observation %d", i)
	}
	allocation.DoLog = new(false)
	h.evaluate(t, "athlete", nil)
	assert.False(t, rows[len(rows)-1].IsExposure())
	assert.False(t, rows[len(rows)-1].ShouldSend())
	assert.False(t, rows[len(rows)-1].IsCacheHit())
	_, err := h.client.StringValue(context.Background(), "experiment", "default", of.NewEvaluationContext("athlete", nil))
	require.Error(t, err)
	_, err = h.client.BooleanValue(context.Background(), "missing", false, of.NewEvaluationContext("athlete", nil))
	require.Error(t, err)
	require.Len(t, rows, 13, "Finally callbacks include provider errors")
	for _, row := range rows[11:] {
		assert.False(t, row.IsExposure())
		assert.False(t, row.IsCacheHit())
		assert.False(t, row.ShouldSend())
		assert.NotEmpty(t, row.Details.ErrorCode)
	}
}

type rejectExposureResultHook struct{ of.UnimplementedHook }

func (rejectExposureResultHook) After(context.Context, of.HookContext, of.InterfaceEvaluationDetails, of.HookHints) error {
	return errors.New("application rejected the flag result")
}

func TestCustomerExposureHookAfterFailureDoesNotConsumeCache(t *testing.T) {
	internalconfig.SetUseFreshConfig(true)
	t.Cleanup(func() { internalconfig.SetUseFreshConfig(false); internalconfig.CreateNew() })
	for _, send := range []bool{true, false} {
		t.Run(fmt.Sprint(send), func(t *testing.T) {
			t.Setenv("DD_FEATURE_FLAGS_EXPOSURES_DATADOG_LOGGING_ENABLED", fmt.Sprint(send))
			h := newOptOutHarness(t, ProviderConfig{})
			var rows []ExposureEvaluation
			h.client.AddHooks(NewExposureHook(func(e ExposureEvaluation) { rows = append(rows, e) }))
			value, err := h.client.BooleanValue(context.Background(), "experiment", false,
				of.NewEvaluationContext("athlete", nil), of.WithHooks(rejectExposureResultHook{}))
			require.Error(t, err)
			assert.False(t, value)
			require.Len(t, rows, 1)
			assert.Equal(t, false, rows[0].Details.Value)
			assert.Equal(t, of.GeneralCode, rows[0].Details.ErrorCode)
			assert.Equal(t, of.ErrorReason, rows[0].Details.Reason)
			assert.False(t, rows[0].IsExposure())
			assert.False(t, rows[0].ShouldSend())
			assert.False(t, rows[0].IsCacheHit())
			h.provider.exposureWriter.flush()
			assert.Empty(t, h.sink.exposures)

			h.evaluate(t, "athlete", nil)
			h.evaluate(t, "athlete", nil)
			require.Len(t, rows, 3)
			assert.True(t, rows[1].ShouldSend(), "the rejected evaluation must not consume the cache")
			assert.False(t, rows[1].IsCacheHit())
			assert.False(t, rows[2].ShouldSend())
			assert.True(t, rows[2].IsCacheHit())
			h.provider.exposureWriter.flush()
			want := 0
			if send {
				want = 1
			}
			assert.Len(t, h.sink.exposures, want)
		})
	}
}

func TestCustomerExposureHookPanicsDoNotAffectDelivery(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{})
	var calls int
	h.client.AddHooks(NewExposureHook(func(ExposureEvaluation) { calls++ }))
	h.client.AddHooks(NewExposureHook(func(ExposureEvaluation) { panic("customer data must not be logged") }))
	h.client.AddHooks(NewExposureHook(nil))
	h.evaluate(t, "athlete", nil)
	h.evaluate(t, "athlete", nil)
	assert.Equal(t, 2, calls)
	h.assertCounts(t, 2, 1, 2)
}

func TestCustomerExposureHookConcurrentDecisions(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{})
	var sends, calls atomic.Int64
	h.client.AddHooks(NewExposureHook(func(e ExposureEvaluation) {
		calls.Add(1)
		if e.ShouldSend() {
			sends.Add(1)
		}
	}))
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			_, err := h.client.BooleanValue(context.Background(), "experiment", false, of.NewEvaluationContext("same-subject", nil))
			assert.NoError(t, err)
		})
	}
	wg.Wait()
	assert.Equal(t, int64(100), calls.Load())
	assert.Equal(t, int64(1), sends.Load())
	h.provider.exposureWriter.flush()
	assert.Len(t, h.sink.exposures, 1)
}

func TestExposureEvaluationRequiresExplicitCacheDecision(t *testing.T) {
	e := ExposureEvaluation{Details: of.InterfaceEvaluationDetails{
		EvaluationDetails: of.EvaluationDetails{ResolutionDetail: of.ResolutionDetail{
			Variant: "on", FlagMetadata: of.FlagMetadata{metadataAllocationKey: "allocation", metadataDoLogKey: true},
		}},
	}}
	assert.True(t, e.IsExposure())
	assert.False(t, e.ShouldSend())
	assert.False(t, e.IsCacheHit())
}
