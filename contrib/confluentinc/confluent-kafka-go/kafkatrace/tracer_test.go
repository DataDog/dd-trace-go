// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafkatrace

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

var testInstr = &instrumentation.Instrumentation{}

func TestDataStreamsActivation(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		tr := NewKafkaTracer(testInstr, 0, 0)
		assert.False(t, tr.DSMEnabled())
	})
	t.Run("withOption", func(t *testing.T) {
		tr := NewKafkaTracer(testInstr, 0, 0, WithDataStreams())
		assert.True(t, tr.DSMEnabled())
	})
	t.Run("withEnv", func(t *testing.T) {
		t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
		tr := NewKafkaTracer(testInstr, 0, 0)
		assert.True(t, tr.DSMEnabled())
	})
	t.Run("optionOverridesEnv", func(t *testing.T) {
		t.Setenv("DD_DATA_STREAMS_ENABLED", "false")
		tr := NewKafkaTracer(testInstr, 0, 0, WithDataStreams())
		assert.True(t, tr.DSMEnabled())
	})
}

func TestAnalyticsSettings(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		tr := NewKafkaTracer(testInstr, 0, 0)
		assert.True(t, math.IsNaN(tr.analyticsRate))
	})

	t.Run("global", func(t *testing.T) {
		t.Skip("global flag disabled")
		testutils.SetGlobalAnalyticsRate(t, 0.4)

		tr := NewKafkaTracer(testInstr, 0, 0)
		assert.Equal(t, 0.4, tr.analyticsRate)
	})

	t.Run("enabled", func(t *testing.T) {
		tr := NewKafkaTracer(testInstr, 0, 0, WithAnalytics(true))
		assert.Equal(t, 1.0, tr.analyticsRate)
	})

	t.Run("override", func(t *testing.T) {
		testutils.SetGlobalAnalyticsRate(t, 0.4)

		tr := NewKafkaTracer(testInstr, 0, 0, WithAnalyticsRate(0.2))
		assert.Equal(t, 0.2, tr.analyticsRate)
	})
}
