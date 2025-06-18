// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetrytest

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/transport"
)

func TestGlobalClient(t *testing.T) {
	t.Run("config", func(t *testing.T) {
		recorder := new(RecordClient)
		defer telemetry.MockClient(recorder)()

		telemetry.RegisterAppConfig("key", "value", telemetry.OriginCode)
		assert.Len(t, recorder.Configuration, 1)
		assert.Equal(t, "key", recorder.Configuration[0].Name)
		assert.Equal(t, "value", recorder.Configuration[0].Value)
		assert.Equal(t, telemetry.OriginCode, recorder.Configuration[0].Origin)
	})

	t.Run("configs", func(t *testing.T) {
		recorder := new(RecordClient)
		defer telemetry.MockClient(recorder)()

		telemetry.RegisterAppConfigs(telemetry.Configuration{Name: "key", Value: "value", Origin: telemetry.OriginCode}, telemetry.Configuration{Name: "key2", Value: "value2", Origin: telemetry.OriginRemoteConfig})
		assert.Len(t, recorder.Configuration, 2)
		assert.Equal(t, "key", recorder.Configuration[0].Name)
		assert.Equal(t, "value", recorder.Configuration[0].Value)
		assert.Equal(t, telemetry.OriginCode, recorder.Configuration[0].Origin)
		assert.Equal(t, "key2", recorder.Configuration[1].Name)
		assert.Equal(t, "value2", recorder.Configuration[1].Value)
		assert.Equal(t, telemetry.OriginRemoteConfig, recorder.Configuration[1].Origin)
	})

	t.Run("app-stop", func(t *testing.T) {
		recorder := new(RecordClient)
		defer telemetry.MockClient(recorder)()

		telemetry.StopApp()
		assert.True(t, recorder.Stopped)
	})

	t.Run("product-start", func(t *testing.T) {
		recorder := new(RecordClient)
		defer telemetry.MockClient(recorder)()

		telemetry.ProductStarted(telemetry.NamespaceAppSec)
		assert.Len(t, recorder.Products, 1)
		assert.True(t, recorder.Products[telemetry.NamespaceAppSec])
	})

	t.Run("product-stopped", func(t *testing.T) {
		recorder := new(RecordClient)
		defer telemetry.MockClient(recorder)()

		telemetry.ProductStopped(telemetry.NamespaceAppSec)
		assert.Len(t, recorder.Products, 1)
		assert.False(t, recorder.Products[telemetry.NamespaceAppSec])
	})

	t.Run("integration-loaded", func(t *testing.T) {
		recorder := new(RecordClient)
		defer telemetry.MockClient(recorder)()

		telemetry.LoadIntegration("test-integration")
		assert.Len(t, recorder.Integrations, 1)
		assert.Equal(t, "test-integration", recorder.Integrations[0].Name)
	})

	t.Run("mark-integration-as-loaded", func(t *testing.T) {
		recorder := new(RecordClient)
		defer telemetry.MockClient(recorder)()

		telemetry.MarkIntegrationAsLoaded(telemetry.Integration{Name: "test-integration", Version: "1.0.0"})
		assert.Len(t, recorder.Integrations, 1)
		assert.Equal(t, "test-integration", recorder.Integrations[0].Name)
		assert.Equal(t, "1.0.0", recorder.Integrations[0].Version)
	})

	t.Run("count", func(t *testing.T) {
		recorder := new(RecordClient)
		recorder.knownMetrics = true
		defer telemetry.MockClient(recorder)()

		telemetry.Count(telemetry.NamespaceTracers, "init_time", nil).Submit(1)
		assert.Len(t, recorder.Metrics, 1)
		require.Contains(t, recorder.Metrics, MetricKey{Name: "init_time", Namespace: telemetry.NamespaceTracers, Kind: string(transport.CountMetric)})
		assert.Equal(t, 1.0, recorder.Metrics[MetricKey{Name: "init_time", Namespace: telemetry.NamespaceTracers, Kind: string(transport.CountMetric)}].Get())
	})

	t.Run("gauge", func(t *testing.T) {
		recorder := new(RecordClient)
		recorder.knownMetrics = true
		defer telemetry.MockClient(recorder)()

		telemetry.Gauge(telemetry.NamespaceTracers, "init_time", nil).Submit(1)
		assert.Len(t, recorder.Metrics, 1)
		require.Contains(t, recorder.Metrics, MetricKey{Name: "init_time", Namespace: telemetry.NamespaceTracers, Kind: string(transport.GaugeMetric)})
		assert.Equal(t, 1.0, recorder.Metrics[MetricKey{Name: "init_time", Namespace: telemetry.NamespaceTracers, Kind: string(transport.GaugeMetric)}].Get())
	})

	t.Run("rate", func(t *testing.T) {
		recorder := new(RecordClient)
		recorder.knownMetrics = true
		defer telemetry.MockClient(recorder)()

		telemetry.Rate(telemetry.NamespaceTracers, "init_time", nil).Submit(1)

		assert.Len(t, recorder.Metrics, 1)
		require.Contains(t, recorder.Metrics, MetricKey{Name: "init_time", Namespace: telemetry.NamespaceTracers, Kind: string(transport.RateMetric)})
		assert.False(t, math.IsNaN(recorder.Metrics[MetricKey{Name: "init_time", Namespace: telemetry.NamespaceTracers, Kind: string(transport.RateMetric)}].Get()))
	})

	t.Run("distribution", func(t *testing.T) {
		recorder := new(RecordClient)
		recorder.knownMetrics = true
		defer telemetry.MockClient(recorder)()

		telemetry.Distribution(telemetry.NamespaceGeneral, "init_time", nil).Submit(1)
		assert.Len(t, recorder.Metrics, 1)
		require.Contains(t, recorder.Metrics, MetricKey{Name: "init_time", Namespace: telemetry.NamespaceGeneral, Kind: string(transport.DistMetric)})
		assert.Equal(t, 1.0, recorder.Metrics[MetricKey{Name: "init_time", Namespace: telemetry.NamespaceGeneral, Kind: string(transport.DistMetric)}].Get())
	})
}
