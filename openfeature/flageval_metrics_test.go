// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// setupTestMetrics creates a flagEvalMetrics with an in-memory ManualReader
// for testing purposes. The returned reader can be used to collect metric data.
func setupTestMetrics(t *testing.T) (*flagEvalMetrics, *metric.ManualReader) {
	t.Helper()
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))

	meter := mp.Meter(meterName)
	counter, err := meter.Int64Counter(
		metricName,
		otelmetric.WithUnit(metricUnit),
		otelmetric.WithDescription(metricDesc),
	)
	if err != nil {
		t.Fatalf("failed to create counter: %v", err)
	}

	return &flagEvalMetrics{
		meterProvider: mp,
		counter:       counter,
	}, reader
}

// collectMetrics collects the current metric data from the reader.
func collectMetrics(t *testing.T, reader *metric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	err := reader.Collect(context.Background(), &rm)
	if err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}
	return rm
}

// findCounter finds the counter data points in the collected metrics.
func findCounter(t *testing.T, rm metricdata.ResourceMetrics) []metricdata.DataPoint[int64] {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == metricName {
				if sum, ok := m.Data.(metricdata.Sum[int64]); ok {
					return sum.DataPoints
				}
			}
		}
	}
	return nil
}

// getAttr returns the string value of an attribute from a data point, or empty string.
func getAttr(dp metricdata.DataPoint[int64], key attribute.Key) string {
	val, ok := dp.Attributes.Value(key)
	if !ok {
		return ""
	}
	return val.AsString()
}

// makeDetails constructs an InterfaceEvaluationDetails for testing record().
// An optional FlagMetadata map can be provided as the last argument.
func makeDetails(variant string, reason of.Reason, errorCode of.ErrorCode, metadata ...of.FlagMetadata) of.InterfaceEvaluationDetails {
	d := of.InterfaceEvaluationDetails{
		EvaluationDetails: of.EvaluationDetails{
			ResolutionDetail: of.ResolutionDetail{
				Variant:   variant,
				Reason:    reason,
				ErrorCode: errorCode,
			},
		},
	}
	if len(metadata) > 0 {
		d.FlagMetadata = metadata[0]
	}
	return d
}

func TestRecord(t *testing.T) {
	tests := []struct {
		name           string
		flagKey        string
		details        of.InterfaceEvaluationDetails
		wantValue      int64
		wantReason     string
		wantVariant    string
		wantError      string // empty means no error.type attribute expected
		wantAllocation string // empty means no allocation_key attribute expected
	}{
		{
			name:        "success with targeting match",
			flagKey:     "my-flag",
			details:     makeDetails("variant-a", of.TargetingMatchReason, ""),
			wantValue:   1,
			wantReason:  "targeting_match",
			wantVariant: "variant-a",
		},
		{
			name:    "success with allocation key",
			flagKey: "my-flag",
			details: makeDetails("variant-a", of.TargetingMatchReason, "", of.FlagMetadata{
				metadataAllocationKey: "default-allocation",
			}),
			wantValue:      1,
			wantReason:     "targeting_match",
			wantVariant:    "variant-a",
			wantAllocation: "default-allocation",
		},
		{
			name:    "empty allocation key omitted",
			flagKey: "my-flag",
			details: makeDetails("variant-a", of.TargetingMatchReason, "", of.FlagMetadata{
				metadataAllocationKey: "",
			}),
			wantValue:   1,
			wantReason:  "targeting_match",
			wantVariant: "variant-a",
		},
		{
			name:        "error flag not found",
			flagKey:     "missing-flag",
			details:     makeDetails("", of.ErrorReason, of.FlagNotFoundCode),
			wantValue:   1,
			wantReason:  "error",
			wantVariant: "",
			wantError:   "flag_not_found",
		},
		{
			name:        "default reason",
			flagKey:     "my-flag",
			details:     makeDetails("", of.DefaultReason, ""),
			wantValue:   1,
			wantReason:  "default",
			wantVariant: "",
		},
		{
			name:        "disabled flag",
			flagKey:     "disabled-flag",
			details:     makeDetails("", of.DisabledReason, ""),
			wantValue:   1,
			wantReason:  "disabled",
			wantVariant: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, reader := setupTestMetrics(t)
			m.record(context.Background(), tc.flagKey, tc.details)

			rm := collectMetrics(t, reader)
			dps := findCounter(t, rm)

			if len(dps) != 1 {
				t.Fatalf("expected 1 data point, got %d", len(dps))
			}

			dp := dps[0]
			if dp.Value != tc.wantValue {
				t.Errorf("value: got %d, want %d", dp.Value, tc.wantValue)
			}
			if got := getAttr(dp, attrFlagKey); got != tc.flagKey {
				t.Errorf("flag key: got %q, want %q", got, tc.flagKey)
			}
			if got := getAttr(dp, attrVariant); got != tc.wantVariant {
				t.Errorf("variant: got %q, want %q", got, tc.wantVariant)
			}
			if got := getAttr(dp, attrReason); got != tc.wantReason {
				t.Errorf("reason: got %q, want %q", got, tc.wantReason)
			}
			if tc.wantError == "" {
				if _, ok := dp.Attributes.Value(attrErrorType); ok {
					t.Error("expected no error.type attribute on successful evaluation")
				}
			} else {
				if got := getAttr(dp, attrErrorType); got != tc.wantError {
					t.Errorf("error.type: got %q, want %q", got, tc.wantError)
				}
			}
			if tc.wantAllocation == "" {
				if _, ok := dp.Attributes.Value(attrAllocationKey); ok {
					t.Error("expected no allocation_key attribute")
				}
			} else {
				if got := getAttr(dp, attrAllocationKey); got != tc.wantAllocation {
					t.Errorf("allocation_key: got %q, want %q", got, tc.wantAllocation)
				}
			}
		})
	}
}

func TestRecordMultipleEvaluations(t *testing.T) {
	m, reader := setupTestMetrics(t)
	ctx := context.Background()

	details := makeDetails("variant-a", of.TargetingMatchReason, "")
	for range 5 {
		m.record(ctx, "my-flag", details)
	}

	rm := collectMetrics(t, reader)
	dps := findCounter(t, rm)

	if len(dps) != 1 {
		t.Fatalf("expected 1 aggregated data point, got %d", len(dps))
	}
	if dps[0].Value != 5 {
		t.Errorf("expected counter value 5, got %d", dps[0].Value)
	}
}

func TestRecordDifferentFlags(t *testing.T) {
	m, reader := setupTestMetrics(t)
	ctx := context.Background()

	m.record(ctx, "flag-a", makeDetails("on", of.TargetingMatchReason, ""))
	m.record(ctx, "flag-b", makeDetails("off", of.DefaultReason, ""))

	rm := collectMetrics(t, reader)
	dps := findCounter(t, rm)

	if len(dps) != 2 {
		t.Fatalf("expected 2 data points (different attribute sets), got %d", len(dps))
	}

	flags := map[string]bool{}
	for _, dp := range dps {
		flags[getAttr(dp, attrFlagKey)] = true
	}
	if !flags["flag-a"] || !flags["flag-b"] {
		t.Errorf("expected both flag-a and flag-b, got %v", flags)
	}
}

func TestRecordAllErrorTypes(t *testing.T) {
	m, reader := setupTestMetrics(t)
	ctx := context.Background()

	errorCases := []struct {
		code     of.ErrorCode
		expected string
	}{
		{of.FlagNotFoundCode, "flag_not_found"},
		{of.TypeMismatchCode, "type_mismatch"},
		{of.ParseErrorCode, "parse_error"},
		{of.GeneralCode, "general"},
		{of.ProviderNotReadyCode, "provider_not_ready"},
	}

	for _, tc := range errorCases {
		m.record(ctx, "test-flag", makeDetails("", of.ErrorReason, tc.code))
	}

	rm := collectMetrics(t, reader)
	dps := findCounter(t, rm)

	if len(dps) != len(errorCases) {
		t.Fatalf("expected %d data points, got %d", len(errorCases), len(dps))
	}

	errorTypes := map[string]bool{}
	for _, dp := range dps {
		errorTypes[getAttr(dp, attrErrorType)] = true
	}
	for _, tc := range errorCases {
		if !errorTypes[tc.expected] {
			t.Errorf("expected error type %q in data points", tc.expected)
		}
	}
}

func TestRecordUnknownReasonFallback(t *testing.T) {
	m, reader := setupTestMetrics(t)
	ctx := context.Background()

	// Record with empty reason - should fall back to "unknown"
	m.record(ctx, "test-flag", makeDetails("variant-a", "", ""))

	rm := collectMetrics(t, reader)
	dps := findCounter(t, rm)

	if len(dps) != 1 {
		t.Fatalf("expected 1 data point, got %d", len(dps))
	}

	if got := getAttr(dps[0], attrReason); got != "unknown" {
		t.Errorf("reason: got %q, want %q", got, "unknown")
	}
}

// TestIntegrationEvaluate tests that the flag evaluation hook correctly records
// metrics when evaluations flow through the full OpenFeature client lifecycle.
func TestIntegrationEvaluate(t *testing.T) {
	t.Run("targeting match records metric via hook", func(t *testing.T) {
		provider := newDatadogProvider(ProviderConfig{})
		provider.updateConfiguration(createTestConfig())

		m, reader := setupTestMetrics(t)
		provider.flagEvalHook.metrics = m

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		domain := "test-eval-match"
		if err := of.SetNamedProviderWithContextAndWait(ctx, domain, provider); err != nil {
			t.Fatalf("failed to set provider: %v", err)
		}

		client := of.NewClient(domain)
		evalCtx := of.NewEvaluationContext("user-123", map[string]any{
			"country": "US",
		})

		_, err := client.BooleanValue(ctx, "bool-flag", false, evalCtx)
		if err != nil {
			t.Fatalf("evaluation failed: %v", err)
		}

		rm := collectMetrics(t, reader)
		dps := findCounter(t, rm)

		if len(dps) != 1 {
			t.Fatalf("expected 1 metric data point, got %d", len(dps))
		}

		dp := dps[0]
		if got := getAttr(dp, attrFlagKey); got != "bool-flag" {
			t.Errorf("flag key: got %q, want %q", got, "bool-flag")
		}
		if got := getAttr(dp, attrReason); got != "targeting_match" {
			t.Errorf("reason: got %q, want %q", got, "targeting_match")
		}
		if got := getAttr(dp, attrVariant); got != "on" {
			t.Errorf("variant: got %q, want %q", got, "on")
		}
		if _, ok := dp.Attributes.Value(attrErrorType); ok {
			t.Error("expected no error.type attribute on successful evaluation")
		}
		if got := getAttr(dp, attrAllocationKey); got != "allocation1" {
			t.Errorf("allocation_key: got %q, want %q", got, "allocation1")
		}
	})

	t.Run("flag not found records error metric via hook", func(t *testing.T) {
		provider := newDatadogProvider(ProviderConfig{})
		provider.updateConfiguration(createTestConfig())

		m, reader := setupTestMetrics(t)
		provider.flagEvalHook.metrics = m

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		domain := "test-eval-notfound"
		if err := of.SetNamedProviderWithContextAndWait(ctx, domain, provider); err != nil {
			t.Fatalf("failed to set provider: %v", err)
		}

		client := of.NewClient(domain)
		evalCtx := of.NewEvaluationContext("user-123", nil)

		// Evaluate a non-existent flag — should record flag_not_found error
		_, _ = client.StringValue(ctx, "non-existent-flag", "default", evalCtx)

		rm := collectMetrics(t, reader)
		dps := findCounter(t, rm)

		if len(dps) != 1 {
			t.Fatalf("expected 1 metric data point, got %d", len(dps))
		}

		dp := dps[0]
		if got := getAttr(dp, attrFlagKey); got != "non-existent-flag" {
			t.Errorf("flag key: got %q, want %q", got, "non-existent-flag")
		}
		if got := getAttr(dp, attrReason); got != "error" {
			t.Errorf("reason: got %q, want %q", got, "error")
		}
		if got := getAttr(dp, attrErrorType); got != "flag_not_found" {
			t.Errorf("error.type: got %q, want %q", got, "flag_not_found")
		}
		if _, ok := dp.Attributes.Value(attrAllocationKey); ok {
			t.Error("expected no allocation_key attribute on flag_not_found error")
		}
	})

	// This test proves the hook catches type conversion errors that the old
	// evaluate()-level recording approach missed. string-flag returns a string
	// value, but we call BooleanValue which triggers a type mismatch error
	// AFTER evaluate() returns.
	t.Run("type conversion error records type_mismatch metric via hook", func(t *testing.T) {
		provider := newDatadogProvider(ProviderConfig{})
		provider.updateConfiguration(createTestConfig())

		m, reader := setupTestMetrics(t)
		provider.flagEvalHook.metrics = m

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		domain := "test-eval-typemismatch"
		if err := of.SetNamedProviderWithContextAndWait(ctx, domain, provider); err != nil {
			t.Fatalf("failed to set provider: %v", err)
		}

		client := of.NewClient(domain)
		evalCtx := of.NewEvaluationContext("user-123", map[string]any{
			"age": 25,
		})

		// string-flag returns "version-2" (string), but we request a boolean.
		// The type mismatch happens in BooleanEvaluation after evaluate() returns.
		_, _ = client.BooleanValue(ctx, "string-flag", false, evalCtx)

		rm := collectMetrics(t, reader)
		dps := findCounter(t, rm)

		if len(dps) != 1 {
			t.Fatalf("expected 1 metric data point, got %d", len(dps))
		}

		dp := dps[0]
		if got := getAttr(dp, attrFlagKey); got != "string-flag" {
			t.Errorf("flag key: got %q, want %q", got, "string-flag")
		}
		if got := getAttr(dp, attrReason); got != "error" {
			t.Errorf("reason: got %q, want %q", got, "error")
		}
		if got := getAttr(dp, attrErrorType); got != "type_mismatch" {
			t.Errorf("error.type: got %q, want %q", got, "type_mismatch")
		}
	})
}
