// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	"testing"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// TestDeltaTemporalitySelector verifies temporality selection per OTel spec:
// - Monotonic instruments (Counter, Histogram, ObservableCounter) → Delta
// - Non-monotonic instruments (UpDownCounter, ObservableUpDownCounter, ObservableGauge) → Cumulative
// - OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE overrides for monotonic instruments only
func TestDeltaTemporalitySelector(t *testing.T) {
	t.Run("Default behavior (no env var set)", func(t *testing.T) {
		selector := deltaTemporalitySelector()

		// Test temporality for each instrument kind per OTel spec:
		// - Monotonic instruments (Counter, ObservableCounter, Histogram) → Delta
		// - Non-monotonic instruments (UpDownCounter, ObservableUpDownCounter, ObservableGauge) → Cumulative
		tests := []struct {
			name                string
			kind                metric.InstrumentKind
			expectedTemporality metricdata.Temporality
		}{
			// Monotonic instruments - should use Delta
			{"Counter", metric.InstrumentKindCounter, metricdata.DeltaTemporality},
			{"Histogram", metric.InstrumentKindHistogram, metricdata.DeltaTemporality},
			{"ObservableCounter", metric.InstrumentKindObservableCounter, metricdata.DeltaTemporality},

			// Non-monotonic instruments - should use Cumulative
			{"UpDownCounter", metric.InstrumentKindUpDownCounter, metricdata.CumulativeTemporality},
			{"ObservableUpDownCounter", metric.InstrumentKindObservableUpDownCounter, metricdata.CumulativeTemporality},
			{"ObservableGauge", metric.InstrumentKindObservableGauge, metricdata.CumulativeTemporality},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				temporality := selector(tt.kind)
				assert.Equal(t, tt.expectedTemporality, temporality, "Incorrect temporality for %s", tt.name)
			})
		}
	})

	t.Run("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=CUMULATIVE", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "CUMULATIVE")
		selector := deltaTemporalitySelector()

		// All instruments should use cumulative when explicitly set
		tests := []metric.InstrumentKind{
			metric.InstrumentKindCounter,
			metric.InstrumentKindHistogram,
			metric.InstrumentKindObservableCounter,
			metric.InstrumentKindUpDownCounter,
			metric.InstrumentKindObservableUpDownCounter,
			metric.InstrumentKindObservableGauge,
		}

		for _, kind := range tests {
			got := selector(kind)
			assert.Equal(t, metricdata.CumulativeTemporality, got, "Expected CUMULATIVE for %v", kind)
		}
	})

	t.Run("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=DELTA", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "DELTA")
		selector := deltaTemporalitySelector()

		// Monotonic instruments should use delta
		deltaTests := []metric.InstrumentKind{
			metric.InstrumentKindCounter,
			metric.InstrumentKindHistogram,
			metric.InstrumentKindObservableCounter,
		}
		for _, kind := range deltaTests {
			got := selector(kind)
			assert.Equal(t, metricdata.DeltaTemporality, got, "Expected DELTA for %v", kind)
		}

		// UpDownCounter and Gauge should ALWAYS use cumulative (even when DELTA is requested)
		cumulativeTests := []metric.InstrumentKind{
			metric.InstrumentKindUpDownCounter,
			metric.InstrumentKindObservableUpDownCounter,
			metric.InstrumentKindObservableGauge,
		}
		for _, kind := range cumulativeTests {
			got := selector(kind)
			assert.Equal(t, metricdata.CumulativeTemporality, got, "Expected CUMULATIVE for %v (even with DELTA preference)", kind)
		}
	})

	t.Run("Case insensitive", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "cumulative")
		selector := deltaTemporalitySelector()

		got := selector(metric.InstrumentKindCounter)
		assert.Equal(t, metricdata.CumulativeTemporality, got)
	})

	t.Run("With whitespace", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "  CUMULATIVE  ")
		selector := deltaTemporalitySelector()

		got := selector(metric.InstrumentKindCounter)
		assert.Equal(t, metricdata.CumulativeTemporality, got)
	})
}

// TestTemporalitySelectorHonored verifies that a user-configured temporality selector
// overrides the hardcoded delta default when passed as the last exporter option.
// This covers the fix that bridges cfg.temporalitySelector → exporter options in
// NewMeterProviderWithContext: the build functions use last-wins ordering, so appending
// the selector after the delta default is sufficient.
func TestTemporalitySelectorHonored(t *testing.T) {
	ctx := context.Background()

	t.Run("cumulative selector overrides delta default for monotonic counter", func(t *testing.T) {
		// Mirror what the fixed NewMeterProviderWithContext does when
		// WithCumulativeTemporality() is set: append the selector as the last option.
		httpOpts := []otlpmetrichttp.Option{
			otlpmetrichttp.WithTemporalitySelector(cumulativeTemporalitySelector()),
		}
		exp, err := newDatadogOTLPExporter(ctx, httpOpts, nil)
		require.NoError(t, err)
		defer exp.Shutdown(ctx) //nolint:errcheck

		assert.Equal(t, metricdata.CumulativeTemporality, exp.Temporality(metric.InstrumentKindCounter))
	})

	t.Run("default (no selector option) gives delta for monotonic counter", func(t *testing.T) {
		exp, err := newDatadogOTLPExporter(ctx, nil, nil)
		require.NoError(t, err)
		defer exp.Shutdown(ctx) //nolint:errcheck

		assert.Equal(t, metricdata.DeltaTemporality, exp.Temporality(metric.InstrumentKindCounter))
	})
}

func TestRuntimeMetricsTransportOptions(t *testing.T) {
	t.Cleanup(func() { internalconfig.CreateNew() })
	requests := make(chan *http.Request, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		requests <- r
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	t.Cleanup(server.Close)
	parent := t
	t.Setenv("DD_METRICS_OTEL_ENABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", server.URL+"/configured")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS", "Authorization=Bearer%20configured")
	internalconfig.CreateNew()

	for _, tc := range []struct {
		name, path, authorization string
		options                   []Option
	}{
		{"override", "/override", "local", []Option{WithHTTPExporter(otlpmetrichttp.WithEndpointURL(server.URL+"/override"), otlpmetrichttp.WithHeaders(map[string]string{"Authorization": "local"}))}},
		{"default after override", "/configured", "Bearer configured", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := append([]Option{WithExportInterval(time.Hour)}, tc.options...)
			mp, err := NewMeterProvider(opts...)
			require.NoError(t, err)
			parent.Cleanup(func() { _ = Shutdown(context.Background(), mp) })
			counter, err := mp.Meter("transport-test").Int64Counter("requests")
			require.NoError(t, err)
			counter.Add(t.Context(), 1)
			require.NoError(t, ForceFlush(t.Context(), mp))
			request := <-requests
			assert.Equal(t, tc.path, request.URL.Path)
			assert.Equal(t, tc.authorization, request.Header.Get("Authorization"))
			assert.Equal(t, "application/x-protobuf", request.Header.Get("Content-Type"))
		})
	}
	assert.Equal(t, map[string]string{"Authorization": "Bearer configured"}, internalconfig.Get().RuntimeMetricsHeaders())
}
