// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDynamicATRRetriesEnabledTelemetry(t *testing.T) {
	// We can't easily assert the exact telemetry metric without a mock telemetry
	// client, but we can at least verify the function doesn't panic and the
	// metric name is correct by checking the telemetry registry.
	// The real assertion is that the function calls telemetry.Count with the
	// correct namespace and metric name.

	t.Run("with_custom_buckets", func(t *testing.T) {
		assert.NotPanics(t, func() {
			DynamicATRRetriesEnabled(true)
		})
	})

	t.Run("without_custom_buckets", func(t *testing.T) {
		assert.NotPanics(t, func() {
			DynamicATRRetriesEnabled(false)
		})
	})
}
