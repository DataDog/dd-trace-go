// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"context"
	"sync"

	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// testExporter is a simple in-memory exporter for testing.
// It captures all exported log records for verification.
type testExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
	stopped bool
}

// newTestExporter creates a new test exporter.
func newTestExporter() *testExporter {
	return &testExporter{
		records: make([]sdklog.Record, 0),
	}
}

// Export captures the log records.
func (e *testExporter) Export(ctx context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.stopped {
		return nil
	}

	// Make a copy of each record to avoid mutation
	for _, r := range records {
		e.records = append(e.records, r)
	}

	return nil
}

// Shutdown shuts down the exporter.
func (e *testExporter) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopped = true
	return nil
}

// ForceFlush is a no-op for the test exporter.
func (e *testExporter) ForceFlush(ctx context.Context) error {
	return nil
}

// GetRecords returns all captured log records.
func (e *testExporter) GetRecords() []sdklog.Record {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Return a copy to avoid external mutation
	result := make([]sdklog.Record, len(e.records))
	copy(result, e.records)
	return result
}

// Reset clears all captured records.
func (e *testExporter) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.records = make([]sdklog.Record, 0)
}
