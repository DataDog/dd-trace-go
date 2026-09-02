// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
)

func TestMLAppLimiterAdmits(t *testing.T) {
	var l mlAppLimiter

	assert.Equal(t, "", l.tagValue(""))

	for i := range maxTelemetryMLApps {
		app := "app-" + strconv.Itoa(i)
		assert.Equal(t, app, l.tagValue(app))
	}

	// Admitted values survive once the limiter is full.
	for i := range maxTelemetryMLApps {
		app := "app-" + strconv.Itoa(i)
		assert.Equal(t, app, l.tagValue(app))
	}

	for i := range 100 {
		assert.Equal(t, telemetryMLAppBlocked, l.tagValue("overflow-"+strconv.Itoa(i)))
	}

	assert.Len(t, *l.admitted.Load(), maxTelemetryMLApps)
}

func TestMLAppLimiterRepeatedValueAdmitsOnce(t *testing.T) {
	var l mlAppLimiter

	for range 10 {
		assert.Equal(t, "same-app", l.tagValue("same-app"))
	}
	assert.Len(t, *l.admitted.Load(), 1)
}

func TestMLAppLimiterConcurrent(t *testing.T) {
	var l mlAppLimiter

	const goroutines = 16
	const perGoroutine = 100

	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Go(func() {
			for i := range perGoroutine {
				// Race admission on the same key and on different keys.
				l.tagValue("shared-" + strconv.Itoa(i))
				l.tagValue("unique-" + strconv.Itoa(g) + "-" + strconv.Itoa(i))
			}
		})
	}
	wg.Wait()

	assert.LessOrEqual(t, len(*l.admitted.Load()), maxTelemetryMLApps)
}

func TestMLAppTelemetryTag(t *testing.T) {
	defer withEmptyMLAppLimiter(t)()

	assert.Equal(t, "ml_app:n/a", mlAppTelemetryTag(""))
	assert.Equal(t, "ml_app:my-app", mlAppTelemetryTag("my-app"))

	for i := range maxTelemetryMLApps {
		mlAppTelemetryTag("app-" + strconv.Itoa(i))
	}
	assert.Equal(t, "ml_app:"+telemetryMLAppBlocked, mlAppTelemetryTag("late-app"))
}

// On the flush path ml_app comes off the serialized span event, not the span.
func TestSpanEventTagsMLAppBounded(t *testing.T) {
	defer withEmptyMLAppLimiter(t)()

	distinct := make(map[string]struct{})
	for i := range maxTelemetryMLApps * 10 {
		event := &transport.LLMObsSpanEvent{
			Meta:   map[string]any{"span.kind": "llm"},
			Tags:   []string{"integration:openai", "ml_app:remote-" + strconv.Itoa(i)},
			Status: transport.SpanStatusOK,
		}
		distinct[findTagValue(spanEventTags(event), "ml_app:")] = struct{}{}
	}

	// maxTelemetryMLApps admitted values, plus the single blocked placeholder.
	assert.Len(t, distinct, maxTelemetryMLApps+1)
	assert.Contains(t, distinct, telemetryMLAppBlocked)
}

func TestCostTagsTelemetryTagsMLAppBounded(t *testing.T) {
	defer withEmptyMLAppLimiter(t)()

	distinct := make(map[string]struct{})
	for i := range maxTelemetryMLApps * 10 {
		span := &Span{mlApp: "remote-" + strconv.Itoa(i)}
		distinct[findTagValue(costTagsTelemetryTags(span, "manual"), "ml_app:")] = struct{}{}
	}

	assert.Len(t, distinct, maxTelemetryMLApps+1)
	assert.Contains(t, distinct, telemetryMLAppBlocked)

	assert.Contains(t, costTagsTelemetryTags(nil, "manual"), "ml_app:N/A")
}

// A filled limiter blocks the ml_app values of every later test, so restore it.
func withEmptyMLAppLimiter(t *testing.T) func() {
	t.Helper()

	previous := telemetryMLApps.admitted.Load()
	telemetryMLApps = mlAppLimiter{}
	require.Nil(t, telemetryMLApps.admitted.Load())

	return func() {
		telemetryMLApps = mlAppLimiter{}
		if previous != nil {
			telemetryMLApps.admitted.Store(previous)
		}
	}
}
