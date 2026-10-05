// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/config/provider"
)

const (
	defaultBLRPMaxQueueSize       = 2048
	defaultBLRPScheduleDelay      = time.Second
	defaultBLRPExportTimeout      = 30 * time.Second
	defaultBLRPMaxExportBatchSize = 512
)

func (c *Config) loadBLRPConfig(p *provider.Provider) {
	positive := func(v int) bool { return v > 0 }
	c.blrpMaxQueueSize = p.GetIntWithValidator("OTEL_BLRP_MAX_QUEUE_SIZE", defaultBLRPMaxQueueSize, positive)
	c.blrpScheduleDelay = time.Duration(p.GetInt64("OTEL_BLRP_SCHEDULE_DELAY", defaultBLRPScheduleDelay.Milliseconds())) * time.Millisecond
	c.blrpExportTimeout = time.Duration(p.GetInt64("OTEL_BLRP_EXPORT_TIMEOUT", defaultBLRPExportTimeout.Milliseconds())) * time.Millisecond
	c.blrpMaxExportBatchSize = p.GetIntWithValidator("OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", defaultBLRPMaxExportBatchSize, positive)
}

func (c *Config) BLRPMaxQueueSize() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.blrpMaxQueueSize
}

func (c *Config) BLRPScheduleDelay() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.blrpScheduleDelay
}

func (c *Config) BLRPExportTimeout() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.blrpExportTimeout
}

func (c *Config) BLRPMaxExportBatchSize() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.blrpMaxExportBatchSize
}
