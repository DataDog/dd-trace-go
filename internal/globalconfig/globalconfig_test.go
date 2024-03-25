// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package globalconfig

import (
	"testing"

	"github.com/DataDog/datadog-go/v5/statsd"
	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/statsdtest"
)

func TestHeaderTag(t *testing.T) {
	SetHeaderTag("header1", "tag1")
	SetHeaderTag("header2", "tag2")

	assert.Equal(t, "tag1", cfg.headersAsTags.Get("header1"))
	assert.Equal(t, "tag2", cfg.headersAsTags.Get("header2"))
}

func TestSetStatsCarrier(t *testing.T) {
	t.Cleanup(ResetGlobalConfig)
	sc := internal.NewStatsCarrier(&statsd.NoOpClient{})
	SetStatsCarrier(sc)
	assert.NotNil(t, cfg.statsCarrier)

}

// Reset globalconfig for running multiple tests
func ResetGlobalConfig() {
	cfg.statsCarrier = nil
}
func TestPushStat(t *testing.T) {
	t.Skip()
	t.Cleanup(ResetGlobalConfig)
	var tg statsdtest.TestStatsdClient
	sc := internal.NewStatsCarrier(&tg)
	sc.Start()
	defer sc.Stop()
	cfg.statsCarrier = sc
	stat := internal.NewGauge("name", float64(1), nil, 1)
	PushStat(stat)
	calls := tg.CallNames()
	assert.Len(t, calls, 1)
	assert.Contains(t, calls, "name")
}

func TestStatsCarrier(t *testing.T) {
	t.Run("default none", func(t *testing.T) {
		assert.False(t, StatsCarrier())
	})
	t.Run("exists", func(t *testing.T) {
		t.Cleanup(ResetGlobalConfig)
		sc := internal.NewStatsCarrier(&statsd.NoOpClient{})
		cfg.statsCarrier = sc
		assert.True(t, StatsCarrier())
	})
}
