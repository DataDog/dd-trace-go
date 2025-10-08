// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package proxy

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
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
				if nbRequests := m.requestCounter.Swap(0); nbRequests > 0 {
					m.logger.Info("analyzed %d requests in the last minute", nbRequests)
				}
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
