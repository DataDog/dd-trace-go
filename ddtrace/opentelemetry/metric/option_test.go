// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package metric

import (
	"context"
	"reflect"
	"testing"
	"time"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestPeriodicReaderAcceptedConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, raw         string
		options           []Option
		interval, timeout time.Duration
	}{
		{"defaults", "", nil, 10 * time.Second, 7500 * time.Millisecond},
		{"environment", "250", nil, 250 * time.Millisecond, 250 * time.Millisecond},
		{"rejected environment", "0", nil, time.Minute, 30 * time.Second},
		{"options", "250", []Option{WithExportInterval(time.Hour), WithExportTimeout(time.Second)}, time.Hour, time.Second},
		{"rejected options", "", []Option{WithExportInterval(0), WithExportTimeout(-1)}, time.Minute, 30 * time.Second},
		{"rejected options use SDK environment", "250", []Option{WithExportInterval(0), WithExportTimeout(-1)}, 250 * time.Millisecond, 250 * time.Millisecond},
		{"SDK does not trim fallback", " 250 ", []Option{WithExportInterval(0), WithExportTimeout(-1)}, time.Minute, 30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(func() { internalconfig.CreateNew() })
			t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", tc.raw)
			t.Setenv("OTEL_METRIC_EXPORT_TIMEOUT", tc.raw)
			internalconfig.CreateNew()
			cfg := newConfig()
			for _, opt := range tc.options {
				opt.apply(cfg)
			}
			exporter, err := newDatadogOTLPExporter(t.Context(), nil, nil)
			require.NoError(t, err)
			reader := newPeriodicReader(exporter, cfg)
			defer reader.Shutdown(context.Background())
			accepted := reflect.ValueOf(reader.MarshalLog())
			assert.Equal(t, tc.interval, accepted.FieldByName("Interval").Interface())
			assert.Equal(t, tc.timeout, accepted.FieldByName("Timeout").Interface())
			assert.Equal(t, cfg.exportInterval, accepted.FieldByName("Interval").Interface())
			assert.Equal(t, cfg.exportTimeout, accepted.FieldByName("Timeout").Interface())
			assert.Equal(t, internalconfig.Get().RuntimeMetricsExportInterval(), newConfig().exportInterval)
		})
	}
}

func TestProviderTemporalityOptions(t *testing.T) {
	t.Cleanup(func() { internalconfig.CreateNew() })
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "LOWMEMORY")
	internalconfig.CreateNew()
	defaultConfig := newConfig()
	WithExportInterval(time.Hour).apply(defaultConfig)
	WithExportTimeout(time.Second).apply(defaultConfig)
	assert.Equal(t, 10*time.Second, newConfig().exportInterval)
	assert.Equal(t, 7500*time.Millisecond, newConfig().exportTimeout)
	for _, tc := range []struct {
		name            string
		option          Option
		counter, upDown metricdata.Temporality
	}{
		{"delta", WithDeltaTemporality(), metricdata.DeltaTemporality, metricdata.CumulativeTemporality},
		{"cumulative", WithCumulativeTemporality(), metricdata.CumulativeTemporality, metricdata.CumulativeTemporality},
		{"custom", WithTemporalitySelector(func(sdkmetric.InstrumentKind) metricdata.Temporality { return metricdata.DeltaTemporality }), metricdata.DeltaTemporality, metricdata.DeltaTemporality},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newConfig()
			tc.option.apply(cfg)
			assert.Equal(t, tc.counter, cfg.temporalitySelector(sdkmetric.InstrumentKindCounter))
			assert.Equal(t, tc.upDown, cfg.temporalitySelector(sdkmetric.InstrumentKindUpDownCounter))
			assert.Nil(t, newConfig().temporalitySelector)
			assert.Equal(t, "delta", internalconfig.Get().RuntimeMetricsTemporalityPreference())
		})
	}
}
