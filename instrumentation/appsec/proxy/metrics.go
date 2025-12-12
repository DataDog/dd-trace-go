// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package proxy

import (
	"context"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

type metrics struct {
	requestCounter atomic.Uint32
	logger         instrumentation.Logger
}

// newMetricsReporter starts a background goroutine to report request metrics
func newMetricsReporter(ctx context.Context, logger instrumentation.Logger) *metrics {
	m := &metrics{
		logger: logger,
	}

	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				m.logger.Info("analyzed %d requests in the last minute", m.requestCounter.Swap(0))
			case <-ctx.Done():
				return
			}
		}
	}()

	return m
}

func (m *metrics) incrementRequestCount() {
	m.requestCounter.Add(1)
}

func EmitBodySize(bodySize int, direction string, truncated bool) {
	telemetry.Distribution(telemetry.NamespaceAppSec, "instrum.body_size", []string{
		"direction:" + direction,
		"truncated:" + strconv.FormatBool(truncated),
	}).Submit(float64(bodySize))
}

func RegisterConfig(mp *Processor) {
	telemetry.RegisterAppConfigs(
		telemetry.Configuration{Name: "appsec.proxy.blockingUnavailable", Value: mp.BlockingUnavailable},
		telemetry.Configuration{Name: "appsec.proxy.bodyParsingSizeLimit", Value: mp.computedBodyParsingSizeLimit.Load()},
		telemetry.Configuration{Name: "appsec.proxy.framework", Value: mp.Framework},
	)
}
