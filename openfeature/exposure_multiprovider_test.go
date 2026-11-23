// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/open-feature/go-sdk/openfeature/multi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
)

func TestExposureMultiProviderGuard(t *testing.T) {
	internalconfig.SetUseFreshConfig(true)
	t.Cleanup(func() { internalconfig.SetUseFreshConfig(false); internalconfig.CreateNew() })
	for _, logging := range []bool{true, false} {
		for _, placement := range []string{"client", "invocation"} {
			t.Run(placement+"/logging="+strconv.FormatBool(logging), func(t *testing.T) {
				t.Setenv("DD_FEATURE_FLAGS_EXPOSURES_DATADOG_LOGGING_ENABLED", strconv.FormatBool(logging))
				h := newOptOutHarness(t, ProviderConfig{})
				var calls int
				customerHook := NewExposureHook(func(ExposureEvaluation) { calls++ })
				mp, err := multi.NewProvider(multi.StrategyFirstMatch, multi.WithProvider("datadog", h.provider, customerHook))
				require.NoError(t, err)
				domain := t.Name() + "/multi"
				require.NoError(t, of.SetNamedProviderAndWait(domain, mp))
				t.Cleanup(mp.Shutdown)
				client := of.NewClient(domain)
				client.AddHooks(customerHook)
				reject := true
				rejectingHook := optOutTestHook{after: func(context.Context, of.HookContext) error {
					if reject {
						return errors.New("application rejected the result")
					}
					return nil
				}}
				var options []of.Option
				if placement == "client" {
					client.AddHooks(rejectingHook)
				} else {
					options = append(options, of.WithHooks(rejectingHook))
				}
				for _, rejected := range []bool{true, false, false} {
					reject = rejected
					result, err := client.BooleanValueDetails(context.Background(), "experiment", false,
						of.NewEvaluationContext("same-subject", nil), options...)
					assert.Equal(t, rejected, err != nil)
					assert.Equal(t, !rejected, result.Value)
					assert.NotContains(t, result.FlagMetadata, metadataExposureCacheHitKey)
					assert.NotContains(t, result.FlagMetadata, metadataExposureAfterErrorKey)
				}
				assert.Zero(t, calls, "both child and outer customer callbacks must be guarded")
				h.provider.exposureWriter.flush()
				assert.Empty(t, h.sink.exposures)

				// A direct evaluation proves that the guarded calls did not consume the cache.
				var rows []ExposureEvaluation
				h.client.AddHooks(NewExposureHook(func(e ExposureEvaluation) { rows = append(rows, e) }))
				h.evaluate(t, "same-subject", nil)
				h.evaluate(t, "same-subject", nil)
				require.Len(t, rows, 2)
				assert.True(t, rows[0].ShouldSend())
				assert.True(t, rows[1].IsCacheHit())
				h.provider.exposureWriter.flush()
				if logging {
					assert.Len(t, h.sink.exposures, 1)
				} else {
					assert.Empty(t, h.sink.exposures)
				}
			})
		}
	}
}

func TestExposureRetainedHooksAreGuardedConcurrently(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{})
	var retained *exposureHook
	for _, hook := range h.provider.Hooks() {
		if exposure, ok := hook.(*exposureHook); ok {
			retained = exposure
		}
	}
	require.NotNil(t, retained)
	var calls atomic.Int64
	customer := NewExposureHook(func(ExposureEvaluation) { calls.Add(1) })
	// Exercise a retained child hook directly. MultiProvider's own context races
	// are outside this guard; the sequential test above covers its real lifecycle.
	ctx := of.NewHookContext("experiment", of.Boolean, false, of.ClientMetadata{},
		of.Metadata{Name: "MultiProvider {datadog: Datadog Provider}"}, of.NewEvaluationContext("subject", nil))
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			details := of.InterfaceEvaluationDetails{Value: true, EvaluationDetails: of.EvaluationDetails{
				FlagKey: "experiment", ResolutionDetail: of.ResolutionDetail{
					Variant: "on", FlagMetadata: of.FlagMetadata{metadataAllocationKey: "allocation", metadataDoLogKey: true},
				},
			}}
			assert.NoError(t, retained.After(context.Background(), ctx, details, of.HookHints{}))
			retained.Error(context.Background(), ctx, errors.New("rejected"), of.HookHints{})
			retained.Finally(context.Background(), ctx, details, of.HookHints{})
			customer.Finally(context.Background(), ctx, details, of.HookHints{})
			assert.NotContains(t, details.FlagMetadata, metadataExposureAfterErrorKey)
			assert.NotContains(t, details.FlagMetadata, metadataExposureCacheHitKey)
		})
	}
	wg.Wait()
	assert.Nil(t, retained.evaluationMetadata)
	assert.Zero(t, calls.Load())
	assert.Empty(t, h.provider.exposureWriter.buffer)
}
