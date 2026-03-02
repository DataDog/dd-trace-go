// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"errors"
	"fmt"
	"testing"

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
		ownsProvider:  false, // test owns the provider
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

func TestRecord(t *testing.T) {
	tests := []struct {
		name        string
		flagKey     string
		variant     string
		reason      string
		err         error
		wantValue   int64
		wantReason  string
		wantVariant string
		wantError   string // empty means no error.type attribute expected
	}{
		{
			name:        "success with targeting match",
			flagKey:     "my-flag",
			variant:     "variant-a",
			reason:      "targeting_match",
			err:         nil,
			wantValue:   1,
			wantReason:  "targeting_match",
			wantVariant: "variant-a",
		},
		{
			name:        "error flag not found",
			flagKey:     "missing-flag",
			variant:     "",
			reason:      "error",
			err:         fmt.Errorf("%w: %q", errFlagNotFound, "missing-flag"),
			wantValue:   1,
			wantReason:  "error",
			wantVariant: "",
			wantError:   "flag_not_found",
		},
		{
			name:        "default reason",
			flagKey:     "my-flag",
			variant:     "",
			reason:      "default",
			err:         nil,
			wantValue:   1,
			wantReason:  "default",
			wantVariant: "",
		},
		{
			name:        "disabled flag",
			flagKey:     "disabled-flag",
			variant:     "",
			reason:      "disabled",
			err:         nil,
			wantValue:   1,
			wantReason:  "disabled",
			wantVariant: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, reader := setupTestMetrics(t)
			m.record(context.Background(), tc.flagKey, tc.variant, tc.reason, tc.err)

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
			if got := getAttr(dp, attrProviderName); got != providerNameAttr {
				t.Errorf("provider name: got %q, want %q", got, providerNameAttr)
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
		})
	}
}

func TestRecordMultipleEvaluations(t *testing.T) {
	m, reader := setupTestMetrics(t)
	ctx := context.Background()

	for range 5 {
		m.record(ctx, "my-flag", "variant-a", "targeting_match", nil)
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

	m.record(ctx, "flag-a", "on", "targeting_match", nil)
	m.record(ctx, "flag-b", "off", "default", nil)

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

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{"flag not found", errFlagNotFound, "flag_not_found"},
		{"wrapped flag not found", fmt.Errorf("context: %w", errFlagNotFound), "flag_not_found"},
		{"type mismatch", errTypeMismatch, "type_mismatch"},
		{"wrapped type mismatch", fmt.Errorf("%w: got string", errTypeMismatch), "type_mismatch"},
		{"parse error", errParseError, "parse_error"},
		{"no configuration", errNoConfiguration, "no_configuration"},
		{"general error", errors.New("some unknown error"), "general"},
		{"context cancelled", context.Canceled, "general"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyError(tc.err); got != tc.expected {
				t.Errorf("classifyError(%v) = %q, want %q", tc.err, got, tc.expected)
			}
		})
	}
}

func TestRecordAllErrorTypes(t *testing.T) {
	m, reader := setupTestMetrics(t)
	ctx := context.Background()

	errorCases := []struct {
		err      error
		expected string
	}{
		{errFlagNotFound, "flag_not_found"},
		{errTypeMismatch, "type_mismatch"},
		{errParseError, "parse_error"},
		{errNoConfiguration, "no_configuration"},
		{errors.New("unknown"), "general"},
	}

	for _, tc := range errorCases {
		m.record(ctx, "test-flag", "", "error", tc.err)
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

func TestShutdownClean(t *testing.T) {
	m, _ := setupTestMetrics(t)
	if err := m.shutdown(context.Background()); err != nil {
		t.Errorf("expected clean shutdown, got error: %v", err)
	}
}

func TestIntegrationEvaluate(t *testing.T) {
	tests := []struct {
		name        string
		flagKey     string
		defaultVal  any
		flatCtx     of.FlattenedContext
		setConfig   bool
		wantReason  string
		wantVariant string
		wantError   string
	}{
		{
			name:       "targeting match records metric",
			flagKey:    "bool-flag",
			defaultVal: false,
			flatCtx: of.FlattenedContext{
				"targetingKey": "user-123",
				"country":      "US",
			},
			setConfig:   true,
			wantReason:  "targeting_match",
			wantVariant: "on",
		},
		{
			name:       "non-existent flag records error metric",
			flagKey:    "non-existent-flag",
			defaultVal: "default",
			flatCtx: of.FlattenedContext{
				"targetingKey": "user-123",
			},
			setConfig:  true,
			wantReason: "error",
			wantError:  "flag_not_found",
		},
		{
			name:       "no configuration records error metric",
			flagKey:    "any-flag",
			defaultVal: "default",
			flatCtx: of.FlattenedContext{
				"targetingKey": "user-123",
			},
			setConfig:  false,
			wantReason: "error",
			wantError:  "no_configuration",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider := newDatadogProvider(ProviderConfig{})
			if tc.setConfig {
				provider.updateConfiguration(createTestConfig())
			}

			m, reader := setupTestMetrics(t)
			provider.flagEvalMetrics = m

			provider.evaluate(context.Background(), tc.flagKey, tc.defaultVal, tc.flatCtx)

			rm := collectMetrics(t, reader)
			dps := findCounter(t, rm)

			if len(dps) != 1 {
				t.Fatalf("expected 1 metric data point, got %d", len(dps))
			}

			dp := dps[0]
			if got := getAttr(dp, attrFlagKey); got != tc.flagKey {
				t.Errorf("flag key: got %q, want %q", got, tc.flagKey)
			}
			if got := getAttr(dp, attrReason); got != tc.wantReason {
				t.Errorf("reason: got %q, want %q", got, tc.wantReason)
			}
			if tc.wantVariant != "" {
				if got := getAttr(dp, attrVariant); got != tc.wantVariant {
					t.Errorf("variant: got %q, want %q", got, tc.wantVariant)
				}
			}
			if tc.wantError != "" {
				if got := getAttr(dp, attrErrorType); got != tc.wantError {
					t.Errorf("error.type: got %q, want %q", got, tc.wantError)
				}
			}
		})
	}
}
