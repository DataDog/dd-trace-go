// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/statsdtest"
)

type IncompatibleStat struct {
	name  string
	value float64
	tags  []string
	rate  float64
}

func NewIncompatibleStat(name string, value float64, tags []string, rate float64) IncompatibleStat {
	return IncompatibleStat{
		name:  name,
		value: value,
		tags:  tags,
		rate:  rate,
	}
}

func (s IncompatibleStat) Name() string {
	return s.name
}

func (s IncompatibleStat) Value() interface{} {
	return s.value
}

func (s IncompatibleStat) Tags() []string {
	return s.tags
}

func (s IncompatibleStat) Rate() float64 {
	return s.rate
}

func TestReportContribMetrics(t *testing.T) {
	t.Run("gauge", func(t *testing.T) {
		var tg statsdtest.TestStatsdClient
		sc := NewStatsCarrier(&tg)
		s := NewGauge("gauge", float64(1), nil, 1)
		sc.Start()
		defer sc.Stop()
		sc.Add(s)
		assert := assert.New(t)
		calls := tg.CallNames()
		assert.Contains(calls, "gauge")
	})
	t.Run("count", func(t *testing.T) {
		var tg statsdtest.TestStatsdClient
		sc := NewStatsCarrier(&tg)
		s := NewCount("count", int64(1), nil, 1)
		sc.Start()
		defer sc.Stop()
		sc.Add(s)
		assert := assert.New(t)
		calls := tg.CallNames()
		assert.Contains(calls, "count")
	})
	t.Run("timing", func(t *testing.T) {
		var tg statsdtest.TestStatsdClient
		sc := NewStatsCarrier(&tg)
		s := NewTiming("timing", 1*time.Second, nil, 1)
		sc.Start()
		defer sc.Stop()
		sc.Add(s)
		assert := assert.New(t)
		calls := tg.CallNames()
		assert.Contains(calls, "timing")
	})
	t.Run("incompatible", func(t *testing.T) {
		var tg statsdtest.TestStatsdClient
		sc := NewStatsCarrier(&tg)
		s := NewIncompatibleStat("incompatible", float64(1), nil, 1)
		sc.Start()
		defer sc.Stop()
		sc.Add(s)
		assert := assert.New(t)
		calls := tg.CallNames()
		assert.Len(calls, 0)
	})
}
