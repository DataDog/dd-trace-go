// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/open-feature/go-sdk/openfeature/isolated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
)

func TestCustomerExposureHooksShareDecision(t *testing.T) {
	internalconfig.SetUseFreshConfig(true)
	t.Cleanup(func() { internalconfig.SetUseFreshConfig(false); internalconfig.CreateNew() })
	tests := []struct {
		name                  string
		datadogLoggingEnabled bool
		wantDatadogExposures  int
	}{
		{name: "datadog_and_customer", datadogLoggingEnabled: true, wantDatadogExposures: 2},
		{name: "customer_only", datadogLoggingEnabled: false, wantDatadogExposures: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DD_FEATURE_FLAGS_EXPOSURES_DATADOG_LOGGING_ENABLED", strconv.FormatBool(tt.datadogLoggingEnabled))
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
			h.assertCounts(t, 3, tt.wantDatadogExposures, 3)
		})
	}
}

func TestCustomerExposureHookSerialIDChanges(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{})
	var rows []ExposureEvaluation
	h.client.AddHooks(NewExposureHook(func(e ExposureEvaluation) { rows = append(rows, e) }))
	allocation := h.provider.configuration.Flags["experiment"].Allocations[0]
	tests := []struct {
		name     string
		serialID *uint32
	}{
		{name: "initial_absent", serialID: nil},
		{name: "absent_to_zero", serialID: new(uint32(0))},
		{name: "zero_to_42", serialID: new(uint32(42))},
		{name: "42_to_zero", serialID: new(uint32(0))},
		{name: "zero_to_absent", serialID: nil},
	}
	// These ordered cases share a cache to verify each serial ID transition.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows = nil
			allocation.Splits[0].SerialID = tt.serialID
			h.evaluate(t, "athlete", nil)
			h.evaluate(t, "athlete", nil)
			require.Len(t, rows, 2)
			assert.True(t, rows[0].ShouldSend())
			assert.False(t, rows[0].IsCacheHit())
			assert.False(t, rows[1].ShouldSend())
			assert.True(t, rows[1].IsCacheHit())
		})
	}
}

func TestCustomerExposureHookNonExposures(t *testing.T) {
	tests := []struct {
		name          string
		doLog         bool
		evaluate      func(*of.Client) error
		wantErrorCode of.ErrorCode
	}{
		{
			name:  "do_log_false",
			doLog: false,
			evaluate: func(client *of.Client) error {
				_, err := client.BooleanValue(context.Background(), "experiment", false, of.NewEvaluationContext("athlete", nil))
				return err
			},
		},
		{
			name:  "type_mismatch",
			doLog: true,
			evaluate: func(client *of.Client) error {
				_, err := client.StringValue(context.Background(), "experiment", "default", of.NewEvaluationContext("athlete", nil))
				return err
			},
			wantErrorCode: of.TypeMismatchCode,
		},
		{
			name:  "missing_flag",
			doLog: true,
			evaluate: func(client *of.Client) error {
				_, err := client.BooleanValue(context.Background(), "missing", false, of.NewEvaluationContext("athlete", nil))
				return err
			},
			wantErrorCode: of.FlagNotFoundCode,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newOptOutHarness(t, ProviderConfig{})
			h.provider.configuration.Flags["experiment"].Allocations[0].DoLog = new(tt.doLog)
			var rows []ExposureEvaluation
			h.client.AddHooks(NewExposureHook(func(e ExposureEvaluation) { rows = append(rows, e) }))

			err := tt.evaluate(h.client)
			if tt.wantErrorCode != "" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Len(t, rows, 1, "Finally callbacks include non-exposures and provider errors")
			assert.Equal(t, tt.wantErrorCode, rows[0].Details.ErrorCode)
			assert.False(t, rows[0].IsExposure())
			assert.False(t, rows[0].IsCacheHit())
			assert.False(t, rows[0].ShouldSend())
			h.provider.exposureWriter.flush()
			assert.Empty(t, h.sink.exposures)
		})
	}
}

type rejectExposureResultHook struct{ of.UnimplementedHook }

func (rejectExposureResultHook) After(context.Context, of.HookContext, of.InterfaceEvaluationDetails, of.HookHints) error {
	return errors.New("application rejected the flag result")
}

func TestCustomerExposureHookAfterFailureDoesNotConsumeCache(t *testing.T) {
	internalconfig.SetUseFreshConfig(true)
	t.Cleanup(func() { internalconfig.SetUseFreshConfig(false); internalconfig.CreateNew() })
	tests := []struct {
		name                  string
		datadogLoggingEnabled bool
		wantDatadogExposures  int
		clientHook            bool
	}{
		{name: "datadog_and_customer", datadogLoggingEnabled: true, wantDatadogExposures: 1},
		{name: "customer_only", datadogLoggingEnabled: false, wantDatadogExposures: 0},
		{name: "client_hook_datadog_and_customer", datadogLoggingEnabled: true, wantDatadogExposures: 1, clientHook: true},
		{name: "client_hook_customer_only", datadogLoggingEnabled: false, wantDatadogExposures: 0, clientHook: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DD_FEATURE_FLAGS_EXPOSURES_DATADOG_LOGGING_ENABLED", strconv.FormatBool(tt.datadogLoggingEnabled))
			h := newOptOutHarness(t, ProviderConfig{})
			var rows []ExposureEvaluation
			h.client.AddHooks(NewExposureHook(func(e ExposureEvaluation) { rows = append(rows, e) }))
			reject := true
			rejectingHook := optOutTestHook{after: func(context.Context, of.HookContext) error {
				if reject {
					return errors.New("application rejected the flag result")
				}
				return nil
			}}
			var options []of.Option
			if tt.clientHook {
				h.client.AddHooks(rejectingHook)
			} else {
				options = append(options, of.WithHooks(rejectingHook))
			}
			details, err := h.client.BooleanValueDetails(context.Background(), "experiment", false,
				of.NewEvaluationContext("athlete", nil), options...)
			require.Error(t, err)
			assert.False(t, details.Value)
			// OpenFeature v1.18.0 returns stale details after an After rejection.
			// TODO(open-feature/go-sdk#623): Require final error details after upgrading.
			assert.Equal(t, of.StaticReason, details.Reason)
			assert.Empty(t, details.ErrorCode)
			assert.Empty(t, details.ErrorMessage)
			assert.Equal(t, "on", details.Variant)
			assert.Equal(t, err.Error(), details.FlagMetadata[metadataExposureAfterErrorKey])
			require.Len(t, rows, 1)
			assert.Equal(t, false, rows[0].Details.Value)
			assert.Equal(t, of.GeneralCode, rows[0].Details.ErrorCode)
			assert.Equal(t, of.ErrorReason, rows[0].Details.Reason)
			assert.Equal(t, err.Error(), rows[0].Details.ErrorMessage)
			assert.NotContains(t, rows[0].Details.FlagMetadata, metadataExposureAfterErrorKey)
			assert.False(t, rows[0].IsExposure())
			assert.False(t, rows[0].ShouldSend())
			assert.False(t, rows[0].IsCacheHit())
			h.provider.exposureWriter.flush()
			assert.Empty(t, h.sink.exposures)

			reject = false
			h.evaluate(t, "athlete", nil)
			h.evaluate(t, "athlete", nil)
			require.Len(t, rows, 3)
			assert.True(t, rows[1].ShouldSend(), "the rejected evaluation must not consume the cache")
			assert.False(t, rows[1].IsCacheHit())
			assert.False(t, rows[2].ShouldSend())
			assert.True(t, rows[2].IsCacheHit())
			h.provider.exposureWriter.flush()
			assert.Len(t, h.sink.exposures, tt.wantDatadogExposures)
		})
	}
}

func TestCustomerExposureHookNestedFailures(t *testing.T) {
	tests := []struct {
		name        string
		rejectOuter bool
		rejectInner bool
	}{
		{name: "outer_rejected", rejectOuter: true},
		{name: "inner_rejected", rejectInner: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newOptOutHarness(t, ProviderConfig{})
			var rows []ExposureEvaluation
			h.client.AddHooks(NewExposureHook(func(e ExposureEvaluation) { rows = append(rows, e) }))
			ctx := context.Background()
			evalContext := of.NewEvaluationContext("same-subject", nil)
			outerHook := optOutTestHook{
				after: func(context.Context, of.HookContext) error {
					var options []of.Option
					if tt.rejectInner {
						options = append(options, of.WithHooks(rejectExposureResultHook{}))
					}
					value, err := h.client.BooleanValue(ctx, "experiment", false, evalContext, options...)
					assert.Equal(t, tt.rejectInner, err != nil)
					assert.Equal(t, !tt.rejectInner, value)
					if tt.rejectOuter {
						return errors.New("outer evaluation rejected")
					}
					return nil
				},
			}
			value, err := h.client.BooleanValue(ctx, "experiment", false, evalContext, of.WithHooks(outerHook))
			assert.Equal(t, tt.rejectOuter, err != nil)
			assert.Equal(t, !tt.rejectOuter, value)
			require.Len(t, rows, 2)
			if tt.rejectInner {
				assert.Equal(t, "after hook: application rejected the flag result", rows[0].Details.ErrorMessage)
				assert.Empty(t, rows[1].Details.ErrorMessage)
			} else {
				assert.Empty(t, rows[0].Details.ErrorMessage)
				assert.Equal(t, "after hook: outer evaluation rejected", rows[1].Details.ErrorMessage)
			}
			assert.Equal(t, !tt.rejectInner, rows[0].ShouldSend())
			assert.Equal(t, !tt.rejectOuter, rows[1].ShouldSend())
			assert.False(t, rows[0].IsCacheHit())
			assert.False(t, rows[1].IsCacheHit())
			h.provider.exposureWriter.flush()
			assert.Len(t, h.sink.exposures, 1)
		})
	}
}

func TestCustomerExposureHookConcurrentFailures(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{})
	var sends, successes, failures atomic.Int64
	h.client.AddHooks(NewExposureHook(func(e ExposureEvaluation) {
		if e.Details.ErrorCode != "" {
			failures.Add(1)
			assert.Equal(t, false, e.Details.Value)
			assert.False(t, e.IsExposure())
			assert.False(t, e.ShouldSend())
			assert.False(t, e.IsCacheHit())
		} else {
			successes.Add(1)
			assert.Equal(t, true, e.Details.Value)
			assert.True(t, e.IsExposure())
		}
		if e.ShouldSend() {
			sends.Add(1)
		}
	}))
	ctx := context.Background()
	evalContext := of.NewEvaluationContext("same-subject", nil)
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			var options []of.Option
			reject := i%2 == 0
			if reject {
				options = append(options, of.WithHooks(rejectExposureResultHook{}))
			}
			value, err := h.client.BooleanValue(ctx, "experiment", false, evalContext, options...)
			assert.Equal(t, reject, err != nil)
			assert.Equal(t, !reject, value)
		})
	}
	wg.Wait()
	assert.Equal(t, int64(50), failures.Load())
	assert.Equal(t, int64(50), successes.Load())
	assert.Equal(t, int64(1), sends.Load())
	h.provider.exposureWriter.flush()
	assert.Len(t, h.sink.exposures, 1)
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

func TestStoppedWriterDoesNotReportCacheHit(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{})
	h.provider.exposureWriter.stop()
	var rows []ExposureEvaluation
	h.client.AddHooks(NewExposureHook(func(e ExposureEvaluation) { rows = append(rows, e) }))
	h.evaluate(t, "subject", nil)
	require.Len(t, rows, 1)
	assert.True(t, rows[0].IsExposure())
	assert.False(t, rows[0].IsCacheHit())
	assert.False(t, rows[0].ShouldSend())
	assert.NotContains(t, rows[0].Details.FlagMetadata, metadataExposureCacheHitKey)
}

func TestExposureDestinationsShareEligibility(t *testing.T) {
	tests := []struct {
		name    string
		variant string
		doLog   any
	}{
		{"empty_variant", "", true},
		{"missing_do_log", "on", nil},
		{"malformed_do_log", "on", "true"},
		{"disabled", "on", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newOptOutHarness(t, ProviderConfig{})
			details := of.InterfaceEvaluationDetails{EvaluationDetails: of.EvaluationDetails{
				FlagKey: "experiment",
				ResolutionDetail: of.ResolutionDetail{Variant: tt.variant, FlagMetadata: of.FlagMetadata{
					metadataAllocationKey: "allocation", metadataDoLogKey: tt.doLog,
				}},
			}}
			hookContext := of.NewHookContext("experiment", of.Boolean, false, of.ClientMetadata{},
				h.provider.Metadata(), of.NewEvaluationContext("subject", nil))
			newExposureHook(h.provider.exposureWriter).Finally(context.Background(), hookContext, details, of.HookHints{})
			assert.False(t, (ExposureEvaluation{Details: details}).IsExposure())
			assert.NotContains(t, details.FlagMetadata, metadataExposureCacheHitKey)
			assert.Empty(t, h.provider.exposureWriter.buffer)
		})
	}
}

func TestCustomerExposureHookCancellation(t *testing.T) {
	for _, before := range []bool{true, false} {
		name := "after_resolution"
		if before {
			name = "before_resolution"
		}
		t.Run(name, func(t *testing.T) {
			h := newOptOutHarness(t, ProviderConfig{})
			var rows []ExposureEvaluation
			h.client.AddHooks(NewExposureHook(func(e ExposureEvaluation) { rows = append(rows, e) }))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if before {
				cancel()
			}
			hook := optOutTestHook{after: func(context.Context, of.HookContext) error { cancel(); return nil }}
			value, err := h.client.BooleanValue(ctx, "experiment", false, of.NewEvaluationContext("subject", nil), of.WithHooks(hook))
			assert.Equal(t, before, err != nil)
			assert.Equal(t, !before, value)
			require.Len(t, rows, 1)
			assert.Equal(t, !before, rows[0].ShouldSend())
			assert.Equal(t, !before, rows[0].IsExposure())
			h.provider.exposureWriter.flush()
			if before {
				assert.Empty(t, h.sink.exposures)
			} else {
				assert.Len(t, h.sink.exposures, 1)
			}
			h.evaluate(t, "subject", nil)
			require.Len(t, rows, 2)
			assert.Equal(t, before, rows[1].ShouldSend(), "only a resolved value consumes the cache")
			assert.Equal(t, !before, rows[1].IsCacheHit())
		})
	}
}

func TestCustomerExposureHookRegistrationLevels(t *testing.T) {
	for _, customerLevel := range []string{"api", "client", "invocation"} {
		for _, rejectionLevel := range []string{"none", "api", "client", "invocation"} {
			t.Run(customerLevel+"/reject_"+rejectionLevel, func(t *testing.T) {
				sink := &optOutSink{}
				evp := newEVPClient()
				evp.httpClient = &http.Client{Transport: sink}
				p := newDatadogProviderWithSourceAndEVP(ProviderConfig{}, internalffe.SourceAgentless, evp)
				var flags universalFlagsConfiguration
				require.NoError(t, json.Unmarshal([]byte(`{"format":"SERVER","environment":{"name":"test"},
                    "flags":{"experiment":{"key":"experiment","enabled":true,"variationType":"BOOLEAN",
                    "variations":{"on":{"key":"on","value":true}},
                    "allocations":[{"key":"allocation","doLog":true,"splits":[{"variationKey":"on","shards":[]}]}]}}}`), &flags))
				p.configuration = &flags
				api := isolated.NewAPI()
				t.Cleanup(func() { require.NoError(t, api.Shutdown(context.Background())) })
				require.NoError(t, api.SetProviderAndWait(context.Background(), p))
				client := api.NewClient()
				var invocation []of.Hook
				register := func(level string, hook of.Hook) {
					switch level {
					case "api":
						api.AddHooks(hook)
					case "client":
						client.AddHooks(hook)
					case "invocation":
						invocation = append(invocation, hook)
					}
				}
				var first, second []ExposureEvaluation
				register(customerLevel, NewExposureHook(func(e ExposureEvaluation) { first = append(first, e) }))
				register(customerLevel, NewExposureHook(func(e ExposureEvaluation) { second = append(second, e) }))
				reject := rejectionLevel != "none"
				register(rejectionLevel, optOutTestHook{after: func(context.Context, of.HookContext) error {
					if reject {
						return errors.New("application rejected the flag result")
					}
					return nil
				}})
				evaluate := func() (bool, error) {
					return client.BooleanValue(context.Background(), "experiment", false, of.NewEvaluationContext("subject", nil), of.WithHooks(invocation...))
				}
				value, err := evaluate()
				assert.Equal(t, reject, err != nil)
				assert.Equal(t, !reject, value)
				p.exposureWriter.flush()
				if reject {
					assert.Empty(t, sink.exposures)
				}
				reject = false
				for range 2 {
					value, err = evaluate()
					require.NoError(t, err)
					assert.True(t, value)
				}
				for _, rows := range [][]ExposureEvaluation{first, second} {
					require.Len(t, rows, 3)
					assert.Equal(t, rejectionLevel == "none", rows[0].IsExposure())
					assert.Equal(t, rejectionLevel == "none", rows[0].ShouldSend())
					assert.False(t, rows[0].IsCacheHit())
					assert.Equal(t, rejectionLevel != "none", rows[1].ShouldSend())
					assert.Equal(t, rejectionLevel == "none", rows[1].IsCacheHit())
					assert.False(t, rows[2].ShouldSend())
					assert.True(t, rows[2].IsCacheHit())
				}
				p.exposureWriter.flush()
				assert.Len(t, sink.exposures, 1)
			})
		}
	}
}

func TestExposureEvaluationAccessors(t *testing.T) {
	for _, tt := range []struct {
		name       string
		metadata   of.FlagMetadata
		allocation string
		hasKey     bool
		serialID   uint32
		hasSerial  bool
	}{
		{name: "absent"},
		{name: "malformed", metadata: of.FlagMetadata{metadataAllocationKey: true, metadataSplitSerialIDKey: "42"}},
		{name: "zero", metadata: of.FlagMetadata{metadataAllocationKey: "allocation", metadataSplitSerialIDKey: uint32(0)}, allocation: "allocation", hasKey: true, hasSerial: true},
		{name: "nonzero", metadata: of.FlagMetadata{metadataAllocationKey: "allocation", metadataSplitSerialIDKey: uint32(42)}, allocation: "allocation", hasKey: true, serialID: 42, hasSerial: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := ExposureEvaluation{Details: of.InterfaceEvaluationDetails{EvaluationDetails: of.EvaluationDetails{
				ResolutionDetail: of.ResolutionDetail{FlagMetadata: tt.metadata},
			}}}
			key, present := e.AllocationKey()
			assert.Equal(t, tt.allocation, key)
			assert.Equal(t, tt.hasKey, present)
			serialID, present := e.SplitSerialID()
			assert.Equal(t, tt.serialID, serialID)
			assert.Equal(t, tt.hasSerial, present)
		})
	}
}

func TestCustomerExposureHookPanicLogging(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{})
	logger := withLogLevel(t, log.LevelDebug)
	first := NewExposureHook(func(ExposureEvaluation) { panic("private callback value") })
	second := NewExposureHook(func(ExposureEvaluation) { panic("private callback value") })
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			value, err := h.client.BooleanValue(context.Background(), "experiment", false, of.NewEvaluationContext("subject", nil), of.WithHooks(first, second))
			assert.NoError(t, err)
			assert.True(t, value)
		})
	}
	wg.Wait()
	assert.Equal(t, 2, logger.countContaining("later panics are logged at debug level"), "each independent hook warns once")
	assert.Equal(t, 40, logger.countContaining(`customer exposure callback panicked for flag "experiment"`))
	assert.Zero(t, logger.countContaining("private callback value"))
}

func TestCustomerExposureHookAfterPanicLimitation(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{})
	var rows []ExposureEvaluation
	h.client.AddHooks(NewExposureHook(func(e ExposureEvaluation) { rows = append(rows, e) }))
	hook := optOutTestHook{after: func(context.Context, of.HookContext) error { panic("application panic") }}
	// OpenFeature v1.18.0 propagates the panic but runs deferred Finally hooks
	// without invoking Error. An application that recovers sees the consumed cache.
	require.PanicsWithValue(t, "application panic", func() {
		_, _ = h.client.BooleanValue(context.Background(), "experiment", false, of.NewEvaluationContext("subject", nil), of.WithHooks(hook))
	})
	h.evaluate(t, "subject", nil)
	require.Len(t, rows, 2)
	assert.True(t, rows[0].ShouldSend())
	assert.True(t, rows[1].IsCacheHit())
}

func TestDisabledExposureDeliveryDoesNotStartWorker(t *testing.T) {
	internalconfig.SetUseFreshConfig(true)
	t.Cleanup(func() { internalconfig.SetUseFreshConfig(false); internalconfig.CreateNew() })
	t.Setenv("DD_FEATURE_FLAGS_EXPOSURES_DATADOG_LOGGING_ENABLED", "false")
	logger := withLogLevel(t, log.LevelInfo)
	h := newOptOutHarness(t, ProviderConfig{})
	w := h.provider.exposureWriter
	w.start()
	assert.Nil(t, w.ticker)
	logger.assertContains(t, "Datadog exposure delivery is disabled; customer exposure hooks and deduplication remain active")
	var rows []ExposureEvaluation
	h.client.AddHooks(NewExposureHook(func(e ExposureEvaluation) { rows = append(rows, e) }))
	h.evaluate(t, "subject", nil)
	h.evaluate(t, "subject", nil)
	require.Len(t, rows, 2)
	assert.True(t, rows[0].ShouldSend())
	assert.True(t, rows[1].IsCacheHit())
	h.assertCounts(t, 2, 0, 2)
	w.stop()
	w.stop()
}
