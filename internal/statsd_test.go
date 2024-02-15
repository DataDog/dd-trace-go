// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// All of this through line 161 is copied from metrics_test.go. Should I permanently move it into this file, then import them in metrics_test.go?
type callType int64

const (
	callTypeGauge callType = iota
	callTypeIncr
	callTypeCount
	callTypeTiming
)

type TestStatsdClient struct {
	mu          sync.RWMutex
	gaugeCalls  []testStatsdCall
	incrCalls   []testStatsdCall
	countCalls  []testStatsdCall
	timingCalls []testStatsdCall
	counts      map[string]int64
	tags        []string
	n           int
	closed      bool
	flushed     int
}

type testStatsdCall struct {
	name     string
	floatVal float64
	intVal   int64
	timeVal  time.Duration
	tags     []string
	rate     float64
}

func (tg *TestStatsdClient) addCount(name string, value int64) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	if tg.counts == nil {
		tg.counts = make(map[string]int64)
	}
	tg.counts[name] += value
}

func (tg *TestStatsdClient) Gauge(name string, value float64, tags []string, rate float64) error {
	return tg.addMetric(callTypeGauge, tags, testStatsdCall{
		name:     name,
		floatVal: value,
		tags:     make([]string, len(tags)),
		rate:     rate,
	})
}

func (tg *TestStatsdClient) Incr(name string, tags []string, rate float64) error {
	tg.addCount(name, 1)
	return tg.addMetric(callTypeIncr, tags, testStatsdCall{
		name: name,
		tags: make([]string, len(tags)),
		rate: rate,
	})
}

func (tg *TestStatsdClient) Count(name string, value int64, tags []string, rate float64) error {
	tg.addCount(name, value)
	return tg.addMetric(callTypeCount, tags, testStatsdCall{
		name:   name,
		intVal: value,
		tags:   make([]string, len(tags)),
		rate:   rate,
	})
}

func (tg *TestStatsdClient) Timing(name string, value time.Duration, tags []string, rate float64) error {
	return tg.addMetric(callTypeTiming, tags, testStatsdCall{
		name:    name,
		timeVal: value,
		tags:    make([]string, len(tags)),
		rate:    rate,
	})
}

func (tg *TestStatsdClient) addMetric(ct callType, tags []string, c testStatsdCall) error {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	copy(c.tags, tags)
	switch ct {
	case callTypeGauge:
		tg.gaugeCalls = append(tg.gaugeCalls, c)
	case callTypeIncr:
		tg.incrCalls = append(tg.incrCalls, c)
	case callTypeCount:
		tg.countCalls = append(tg.countCalls, c)
	case callTypeTiming:
		tg.timingCalls = append(tg.timingCalls, c)
	}
	tg.tags = tags
	tg.n++
	return nil
}

func (tg *TestStatsdClient) Flush() error {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	tg.flushed++
	return nil
}

func (tg *TestStatsdClient) Close() error {
	tg.closed = true
	return nil
}

func (tg *TestStatsdClient) CallNames() []string {
	tg.mu.RLock()
	defer tg.mu.RUnlock()
	var n []string
	for _, c := range tg.gaugeCalls {
		n = append(n, c.name)
	}
	for _, c := range tg.incrCalls {
		n = append(n, c.name)
	}
	for _, c := range tg.countCalls {
		n = append(n, c.name)
	}
	for _, c := range tg.timingCalls {
		n = append(n, c.name)
	}
	return n
}

func (tg *TestStatsdClient) CallsByName() map[string]int {
	tg.mu.RLock()
	defer tg.mu.RUnlock()
	counts := make(map[string]int)
	for _, c := range tg.gaugeCalls {
		counts[c.name]++
	}
	for _, c := range tg.incrCalls {
		counts[c.name]++
	}
	for _, c := range tg.countCalls {
		counts[c.name]++
	}
	for _, c := range tg.timingCalls {
		counts[c.name]++
	}
	return counts
}

func TestReportContribMetrics(t *testing.T) {
	t.Run("gauge", func(t *testing.T) {
		var tg TestStatsdClient
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
		var tg TestStatsdClient
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
		var tg TestStatsdClient
		sc := NewStatsCarrier(&tg)
		s := NewTiming("timing", 1 * time.Second, nil, 1)
		sc.Start()
		defer sc.Stop()
		sc.Add(s)
		assert := assert.New(t)
		calls := tg.CallNames()
		assert.Contains(calls, "timing")
	})
}
