// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"errors"
	"fmt"
	"time"

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
	attrFlagKey   = attribute.Key("feature_flag.key")
	attrVariant   = attribute.Key("feature_flag.result.variant")
	attrReason    = attribute.Key("feature_flag.result.reason")
	attrErrorType = attribute.Key("error.type")
)

// flagEvalMetrics manages OTel metric instruments for flag evaluation tracking.
type flagEvalMetrics struct {
	meterProvider otelmetric.MeterProvider
	counter       otelmetric.Int64Counter
	ownsProvider  bool // true if we created the provider (and must shut it down)
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
		ownsProvider:  true,
	}, nil
}

// record records a single flag evaluation.
func (m *flagEvalMetrics) record(
	ctx context.Context,
	flagKey string,
	variantKey string,
	reason string,
	evalErr error,
) {
	attrs := []attribute.KeyValue{
		attrFlagKey.String(flagKey),
		attrVariant.String(variantKey),
		attrReason.String(reason),
	}

	if evalErr != nil {
		errType := "general"
		for sentinel, tag := range errorTypeTags {
			if errors.Is(evalErr, sentinel) {
				errType = tag
				break
			}
		}
		attrs = append(attrs, attrErrorType.String(errType))
	}

	m.counter.Add(ctx, 1, otelmetric.WithAttributes(attrs...))
}

// errorTypeTags maps sentinel errors to low-cardinality metric tag values.
var errorTypeTags = map[error]string{
	errFlagNotFound:    "flag_not_found",
	errTypeMismatch:    "type_mismatch",
	errParseError:      "parse_error",
	errNoConfiguration: "no_configuration",
}

// shutdown gracefully shuts down the meter provider.
func (m *flagEvalMetrics) shutdown(ctx context.Context) error {
	if m.ownsProvider {
		return ddmetric.Shutdown(ctx, m.meterProvider)
	}
	return nil
}

