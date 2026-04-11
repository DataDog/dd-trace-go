// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"fmt"
	"strings"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"

	ddmetric "github.com/DataDog/dd-trace-go/v2/ddtrace/opentelemetry/metric"
)

const (
	meterName  = "github.com/DataDog/dd-trace-go/openfeature"
	metricName = "feature_flag.evaluations"
	metricUnit = "{evaluation}"
	metricDesc = "Number of feature flag evaluations"
)

// Attribute keys (following OTel semconv naming)
var (
	attrFlagKey       = attribute.Key("feature_flag.key")
	attrVariant       = attribute.Key("feature_flag.result.variant")
	attrReason        = attribute.Key("feature_flag.result.reason")
	attrErrorType     = attribute.Key("error.type")
	attrAllocationKey = attribute.Key("feature_flag.result.allocation_key")
)

// flagEvalHook implements the OpenFeature Hook interface to track flag evaluation metrics.
// It uses the Finally hook stage so that metrics are recorded after all evaluation logic
// completes, including type conversion errors and "not ready" state evaluations.
type flagEvalHook struct {
	of.UnimplementedHook
	metrics *flagEvalMetrics
}

// newFlagEvalHook creates a new flag evaluation metrics hook.
func newFlagEvalHook(m *flagEvalMetrics) *flagEvalHook {
	return &flagEvalHook{metrics: m}
}

// Finally is called after every flag evaluation (success or error).
// It records a metric for the evaluation result.
func (h *flagEvalHook) Finally(
	ctx context.Context,
	hookContext of.HookContext,
	details of.InterfaceEvaluationDetails,
	_ of.HookHints,
) {
	if h.metrics == nil {
		return
	}
	h.metrics.record(ctx, hookContext.FlagKey(), details)
}

// flagEvalMetrics manages OTel metric instruments for flag evaluation tracking.
type flagEvalMetrics struct {
	meterProvider otelmetric.MeterProvider
	counter       otelmetric.Int64Counter
}

// newFlagEvalMetrics creates a new metrics tracker.
// It creates an internal MeterProvider using dd-trace-go's OTel metrics support.
// If DD_METRICS_OTEL_ENABLED is not true, the provider is a noop and
// counter.Add() calls are free.
func newFlagEvalMetrics() (*flagEvalMetrics, error) {
	mp, err := ddmetric.NewMeterProvider(
		ddmetric.WithExportInterval(10 * time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create meter provider: %w", err)
	}

	meter := mp.Meter(meterName)
	counter, err := meter.Int64Counter(
		metricName,
		otelmetric.WithUnit(metricUnit),
		otelmetric.WithDescription(metricDesc),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create counter: %w", err)
	}

	return &flagEvalMetrics{
		meterProvider: mp,
		counter:       counter,
	}, nil
}

// record records a single flag evaluation from the evaluation details.
func (m *flagEvalMetrics) record(
	ctx context.Context,
	flagKey string,
	details of.InterfaceEvaluationDetails,
) {
	// Use "unknown" as fallback for missing reason (matches OpenFeature SDK telemetry convention)
	reason := string(details.Reason)
	if reason == "" {
		reason = "unknown"
	} else {
		reason = strings.ToLower(reason)
	}

	attrs := []attribute.KeyValue{
		attrFlagKey.String(flagKey),
		attrVariant.String(details.Variant),
		attrReason.String(reason),
	}

	// Use raw lowercase error code directly (no conversion function needed)
	if details.ErrorCode != "" {
		attrs = append(attrs, attrErrorType.String(strings.ToLower(string(details.ErrorCode))))
	}

	if ak, ok := details.FlagMetadata[metadataAllocationKey].(string); ok && ak != "" {
		attrs = append(attrs, attrAllocationKey.String(ak))
	}

	m.counter.Add(ctx, 1, otelmetric.WithAttributes(attrs...))
}

// shutdown gracefully shuts down the meter provider.
func (m *flagEvalMetrics) shutdown(ctx context.Context) error {
	return ddmetric.Shutdown(ctx, m.meterProvider)
}
