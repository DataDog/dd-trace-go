// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestResolveBLRPMaxQueueSize(t *testing.T) {
	t.Run("defaults to 2048", func(t *testing.T) {
		size := loadConfig().BLRPMaxQueueSize()
		assert.Equal(t, 2048, size)
	})

	t.Run("uses OTEL_BLRP_MAX_QUEUE_SIZE", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_MAX_QUEUE_SIZE", "4096")
		size := loadConfig().BLRPMaxQueueSize()
		assert.Equal(t, 4096, size)
	})

	t.Run("falls back to default on invalid value", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_MAX_QUEUE_SIZE", "invalid")
		size := loadConfig().BLRPMaxQueueSize()
		assert.Equal(t, 2048, size)
	})

	t.Run("falls back to default on zero", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_MAX_QUEUE_SIZE", "0")
		size := loadConfig().BLRPMaxQueueSize()
		assert.Equal(t, 2048, size)
	})

	t.Run("falls back to default on negative", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_MAX_QUEUE_SIZE", "-100")
		size := loadConfig().BLRPMaxQueueSize()
		assert.Equal(t, 2048, size)
	})
}

func TestResolveBLRPScheduleDelay(t *testing.T) {
	t.Run("defaults to 1000ms", func(t *testing.T) {
		delay := loadConfig().BLRPScheduleDelay()
		assert.Equal(t, 1000*time.Millisecond, delay)
	})

	t.Run("uses OTEL_BLRP_SCHEDULE_DELAY", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_SCHEDULE_DELAY", "500")
		delay := loadConfig().BLRPScheduleDelay()
		assert.Equal(t, 500*time.Millisecond, delay)
	})

	t.Run("falls back to default on invalid value", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_SCHEDULE_DELAY", "invalid")
		delay := loadConfig().BLRPScheduleDelay()
		assert.Equal(t, 1000*time.Millisecond, delay)
	})
}

func TestResolveBLRPExportTimeout(t *testing.T) {
	t.Run("defaults to 30000ms", func(t *testing.T) {
		timeout := loadConfig().BLRPExportTimeout()
		assert.Equal(t, 30000*time.Millisecond, timeout)
	})

	t.Run("uses OTEL_BLRP_EXPORT_TIMEOUT", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_EXPORT_TIMEOUT", "15000")
		timeout := loadConfig().BLRPExportTimeout()
		assert.Equal(t, 15000*time.Millisecond, timeout)
	})

	t.Run("falls back to default on invalid value", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_EXPORT_TIMEOUT", "invalid")
		timeout := loadConfig().BLRPExportTimeout()
		assert.Equal(t, 30000*time.Millisecond, timeout)
	})
}

func TestResolveBLRPMaxExportBatchSize(t *testing.T) {
	t.Run("defaults to 512", func(t *testing.T) {
		size := loadConfig().BLRPMaxExportBatchSize()
		assert.Equal(t, 512, size)
	})

	t.Run("uses OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", "1024")
		size := loadConfig().BLRPMaxExportBatchSize()
		assert.Equal(t, 1024, size)
	})

	t.Run("falls back to default on invalid value", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", "invalid")
		size := loadConfig().BLRPMaxExportBatchSize()
		assert.Equal(t, 512, size)
	})

	t.Run("falls back to default on zero", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", "0")
		size := loadConfig().BLRPMaxExportBatchSize()
		assert.Equal(t, 512, size)
	})

	t.Run("falls back to default on negative", func(t *testing.T) {
		t.Setenv("OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", "-100")
		size := loadConfig().BLRPMaxExportBatchSize()
		assert.Equal(t, 512, size)
	})
}

func TestBLRPConfigTelemetry(t *testing.T) {
	t.Run("reports BLRP configurations", func(t *testing.T) {
		recorder := &telemetrytest.RecordClient{}
		defer telemetry.MockClient(recorder)()

		t.Setenv("OTEL_BLRP_MAX_QUEUE_SIZE", "4096")
		t.Setenv("OTEL_BLRP_SCHEDULE_DELAY", "2000")
		t.Setenv("OTEL_BLRP_EXPORT_TIMEOUT", "60000")
		t.Setenv("OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", "1024")

		loadConfig()

		telemetrytest.CheckConfig(t, recorder.Configuration, "OTEL_BLRP_MAX_QUEUE_SIZE", "4096")
		telemetrytest.CheckConfig(t, recorder.Configuration, "OTEL_BLRP_SCHEDULE_DELAY", "2000")
		telemetrytest.CheckConfig(t, recorder.Configuration, "OTEL_BLRP_EXPORT_TIMEOUT", "60000")
		telemetrytest.CheckConfig(t, recorder.Configuration, "OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", "1024")
	})

	t.Run("reports default values when env vars not set", func(t *testing.T) {
		recorder := &telemetrytest.RecordClient{}
		defer telemetry.MockClient(recorder)()

		loadConfig()

		// Check that defaults are reported with OriginDefault
		var foundMaxQueueSize, foundScheduleDelay, foundExportTimeout, foundMaxBatchSize bool

		for _, cfg := range recorder.Configuration {
			if cfg.Origin != telemetry.OriginDefault {
				continue
			}
			switch cfg.Name {
			case "OTEL_BLRP_MAX_QUEUE_SIZE":
				foundMaxQueueSize = true
				assert.Equal(t, defaultBLRPMaxQueueSize, cfg.Value)
				assert.Equal(t, telemetry.OriginDefault, cfg.Origin)
			case "OTEL_BLRP_SCHEDULE_DELAY":
				foundScheduleDelay = true
				assert.Equal(t, defaultBLRPScheduleDelay.Milliseconds(), cfg.Value)
				assert.Equal(t, telemetry.OriginDefault, cfg.Origin)
			case "OTEL_BLRP_EXPORT_TIMEOUT":
				foundExportTimeout = true
				assert.Equal(t, defaultBLRPExportTimeout.Milliseconds(), cfg.Value)
				assert.Equal(t, telemetry.OriginDefault, cfg.Origin)
			case "OTEL_BLRP_MAX_EXPORT_BATCH_SIZE":
				foundMaxBatchSize = true
				assert.Equal(t, defaultBLRPMaxExportBatchSize, cfg.Value)
				assert.Equal(t, telemetry.OriginDefault, cfg.Origin)
			}
		}

		assert.True(t, foundMaxQueueSize, "expected OTEL_BLRP_MAX_QUEUE_SIZE config")
		assert.True(t, foundScheduleDelay, "expected OTEL_BLRP_SCHEDULE_DELAY config")
		assert.True(t, foundExportTimeout, "expected OTEL_BLRP_EXPORT_TIMEOUT config")
		assert.True(t, foundMaxBatchSize, "expected OTEL_BLRP_MAX_EXPORT_BATCH_SIZE config")
	})
}
