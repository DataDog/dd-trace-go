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

func TestRecordSuccess(t *testing.T) {
	m, reader := setupTestMetrics(t)
	ctx := context.Background()

	m.record(ctx, "my-flag", "variant-a", "targeting_match", nil)

	rm := collectMetrics(t, reader)
	dps := findCounter(t, rm)

	if len(dps) != 1 {
		t.Fatalf("expected 1 data point, got %d", len(dps))
	}

	dp := dps[0]
	if dp.Value != 1 {
		t.Errorf("expected counter value 1, got %d", dp.Value)
	}
	if got := getAttr(dp, attrFlagKey); got != "my-flag" {
		t.Errorf("expected flag key 'my-flag', got %q", got)
	}
	if got := getAttr(dp, attrProviderName); got != providerNameAttr {
		t.Errorf("expected provider name %q, got %q", providerNameAttr, got)
	}
	if got := getAttr(dp, attrVariant); got != "variant-a" {
		t.Errorf("expected variant 'variant-a', got %q", got)
	}
	if got := getAttr(dp, attrReason); got != "targeting_match" {
		t.Errorf("expected reason 'targeting_match', got %q", got)
	}
	// No error.type on success
	if _, ok := dp.Attributes.Value(attrErrorType); ok {
		t.Error("expected no error.type attribute on successful evaluation")
	}
}

func TestRecordError(t *testing.T) {
	m, reader := setupTestMetrics(t)
	ctx := context.Background()

	m.record(ctx, "missing-flag", "", "error", fmt.Errorf("%w: %q", errFlagNotFound, "missing-flag"))

	rm := collectMetrics(t, reader)
	dps := findCounter(t, rm)

	if len(dps) != 1 {
		t.Fatalf("expected 1 data point, got %d", len(dps))
	}

	dp := dps[0]
	if dp.Value != 1 {
		t.Errorf("expected counter value 1, got %d", dp.Value)
	}
	if got := getAttr(dp, attrReason); got != "error" {
		t.Errorf("expected reason 'error', got %q", got)
	}
	if got := getAttr(dp, attrErrorType); got != "flag_not_found" {
		t.Errorf("expected error type 'flag_not_found', got %q", got)
	}
	if got := getAttr(dp, attrVariant); got != "" {
		t.Errorf("expected empty variant, got %q", got)
	}
}

func TestRecordDefault(t *testing.T) {
	m, reader := setupTestMetrics(t)
	ctx := context.Background()

	m.record(ctx, "my-flag", "", "default", nil)

	rm := collectMetrics(t, reader)
	dps := findCounter(t, rm)

	if len(dps) != 1 {
		t.Fatalf("expected 1 data point, got %d", len(dps))
	}

	dp := dps[0]
	if got := getAttr(dp, attrReason); got != "default" {
		t.Errorf("expected reason 'default', got %q", got)
	}
	if got := getAttr(dp, attrVariant); got != "" {
		t.Errorf("expected empty variant, got %q", got)
	}
}

func TestRecordDisabled(t *testing.T) {
	m, reader := setupTestMetrics(t)
	ctx := context.Background()

	m.record(ctx, "disabled-flag", "", "disabled", nil)

	rm := collectMetrics(t, reader)
	dps := findCounter(t, rm)

	if len(dps) != 1 {
		t.Fatalf("expected 1 data point, got %d", len(dps))
	}

	dp := dps[0]
	if got := getAttr(dp, attrReason); got != "disabled" {
		t.Errorf("expected reason 'disabled', got %q", got)
	}
}

func TestRecordMultipleEvaluations(t *testing.T) {
	m, reader := setupTestMetrics(t)
	ctx := context.Background()

	// Record 5 evaluations of the same flag with the same attributes
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

	// Verify both flags are present
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
			got := classifyError(tc.err)
			if got != tc.expected {
				t.Errorf("classifyError(%v) = %q, want %q", tc.err, got, tc.expected)
			}
		})
	}
}

func TestMapReason(t *testing.T) {
	tests := []struct {
		reason   of.Reason
		expected string
	}{
		{of.TargetingMatchReason, "targeting_match"},
		{of.DefaultReason, "default"},
		{of.DisabledReason, "disabled"},
		{of.ErrorReason, "error"},
		{of.Reason("CUSTOM_REASON"), "CUSTOM_REASON"},
	}

	for _, tc := range tests {
		t.Run(string(tc.reason), func(t *testing.T) {
			got := mapReason(tc.reason)
			if got != tc.expected {
				t.Errorf("mapReason(%q) = %q, want %q", tc.reason, got, tc.expected)
			}
		})
	}
}

func TestShutdownClean(t *testing.T) {
	m, _ := setupTestMetrics(t)

	// ownsProvider is false for test metrics, so this should be a noop
	err := m.shutdown(context.Background())
	if err != nil {
		t.Errorf("expected clean shutdown, got error: %v", err)
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

	// Each error type creates a separate data point due to different error.type attributes
	if len(dps) != len(errorCases) {
		t.Fatalf("expected %d data points, got %d", len(errorCases), len(dps))
	}

	// Collect all error types
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

func TestIntegrationEvaluateRecordsMetric(t *testing.T) {
	// Test that provider.evaluate() records metrics
	provider := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()
	provider.updateConfiguration(config)

	// Replace the flagEvalMetrics with a test one
	m, reader := setupTestMetrics(t)
	provider.flagEvalMetrics = m

	ctx := context.Background()

	// Evaluate a flag that should match
	flatCtx := of.FlattenedContext{
		"targetingKey": "user-123",
		"country":      "US",
	}
	result := provider.evaluate(ctx, "bool-flag", false, flatCtx)
	if result.Error != nil {
		t.Fatalf("unexpected evaluation error: %v", result.Error)
	}
	if result.Reason != of.TargetingMatchReason {
		t.Errorf("expected TargetingMatchReason, got %v", result.Reason)
	}

	rm := collectMetrics(t, reader)
	dps := findCounter(t, rm)

	if len(dps) != 1 {
		t.Fatalf("expected 1 metric data point, got %d", len(dps))
	}

	dp := dps[0]
	if got := getAttr(dp, attrFlagKey); got != "bool-flag" {
		t.Errorf("expected flag key 'bool-flag', got %q", got)
	}
	if got := getAttr(dp, attrReason); got != "targeting_match" {
		t.Errorf("expected reason 'targeting_match', got %q", got)
	}
	if got := getAttr(dp, attrVariant); got != "on" {
		t.Errorf("expected variant 'on', got %q", got)
	}
}

func TestIntegrationEvaluateRecordsErrorMetric(t *testing.T) {
	// Test that evaluating a non-existent flag records an error metric
	provider := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()
	provider.updateConfiguration(config)

	m, reader := setupTestMetrics(t)
	provider.flagEvalMetrics = m

	ctx := context.Background()
	flatCtx := of.FlattenedContext{
		"targetingKey": "user-123",
	}

	result := provider.evaluate(ctx, "non-existent-flag", "default", flatCtx)
	if result.Error == nil {
		t.Fatal("expected evaluation error for non-existent flag")
	}

	rm := collectMetrics(t, reader)
	dps := findCounter(t, rm)

	if len(dps) != 1 {
		t.Fatalf("expected 1 metric data point, got %d", len(dps))
	}

	dp := dps[0]
	if got := getAttr(dp, attrFlagKey); got != "non-existent-flag" {
		t.Errorf("expected flag key 'non-existent-flag', got %q", got)
	}
	if got := getAttr(dp, attrReason); got != "error" {
		t.Errorf("expected reason 'error', got %q", got)
	}
	if got := getAttr(dp, attrErrorType); got != "flag_not_found" {
		t.Errorf("expected error type 'flag_not_found', got %q", got)
	}
}

func TestIntegrationNoConfigRecordsMetric(t *testing.T) {
	// Test that evaluating without configuration records the right error metric
	provider := newDatadogProvider(ProviderConfig{})
	// Do NOT set configuration

	m, reader := setupTestMetrics(t)
	provider.flagEvalMetrics = m

	ctx := context.Background()
	flatCtx := of.FlattenedContext{
		"targetingKey": "user-123",
	}

	result := provider.evaluate(ctx, "any-flag", "default", flatCtx)
	if result.Error == nil {
		t.Fatal("expected evaluation error when no configuration loaded")
	}

	rm := collectMetrics(t, reader)
	dps := findCounter(t, rm)

	if len(dps) != 1 {
		t.Fatalf("expected 1 metric data point, got %d", len(dps))
	}

	dp := dps[0]
	if got := getAttr(dp, attrReason); got != "error" {
		t.Errorf("expected reason 'error', got %q", got)
	}
	if got := getAttr(dp, attrErrorType); got != "no_configuration" {
		t.Errorf("expected error type 'no_configuration', got %q", got)
	}
}
