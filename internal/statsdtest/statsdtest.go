// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package statsdtest // import "gopkg.in/DataDog/dd-trace-go.v1/internal/statsdtest"

import (
	"fmt"
	"sync"
	"time"

	"github.com/stretchr/testify/assert"
)

type callType int64

const (
	callTypeGauge callType = iota
	callTypeIncr
	callTypeCount
	callTypeTiming
)

type TestStatsdClient struct {
	mu          sync.RWMutex
	gaugeCalls  []TestStatsdCall
	incrCalls   []TestStatsdCall
	countCalls  []TestStatsdCall
	timingCalls []TestStatsdCall
	counts      map[string]int64
	tags        []string
	n           int
	closed      bool
	flushed     int
}

type TestStatsdCall struct {
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
	return tg.addMetric(callTypeGauge, tags, TestStatsdCall{
		name:     name,
		floatVal: value,
		tags:     make([]string, len(tags)),
		rate:     rate,
	})
}

func (tg *TestStatsdClient) Incr(name string, tags []string, rate float64) error {
	tg.addCount(name, 1)
	return tg.addMetric(callTypeIncr, tags, TestStatsdCall{
		name: name,
		tags: make([]string, len(tags)),
		rate: rate,
	})
}

func (tg *TestStatsdClient) Count(name string, value int64, tags []string, rate float64) error {
	tg.addCount(name, value)
	return tg.addMetric(callTypeCount, tags, TestStatsdCall{
		name:   name,
		intVal: value,
		tags:   make([]string, len(tags)),
		rate:   rate,
	})
}

func (tg *TestStatsdClient) Timing(name string, value time.Duration, tags []string, rate float64) error {
	return tg.addMetric(callTypeTiming, tags, TestStatsdCall{
		name:    name,
		timeVal: value,
		tags:    make([]string, len(tags)),
		rate:    rate,
	})
}

func (tg *TestStatsdClient) addMetric(ct callType, tags []string, c TestStatsdCall) error {
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

func (tg *TestStatsdClient) GaugeCalls() []TestStatsdCall {
	tg.mu.RLock()
	defer tg.mu.RUnlock()
	c := make([]TestStatsdCall, len(tg.gaugeCalls))
	copy(c, tg.gaugeCalls)
	return c
}

func (tg *TestStatsdClient) IncrCalls() []TestStatsdCall {
	tg.mu.RLock()
	defer tg.mu.RUnlock()
	c := make([]TestStatsdCall, len(tg.incrCalls))
	copy(c, tg.incrCalls)
	return c
}

func (tg *TestStatsdClient) CountCalls() []TestStatsdCall {
	tg.mu.RLock()
	defer tg.mu.RUnlock()
	c := make([]TestStatsdCall, len(tg.countCalls))
	copy(c, tg.countCalls)
	return c
}

func (tg *TestStatsdClient) TimingCalls() []TestStatsdCall {
	tg.mu.RLock()
	defer tg.mu.RUnlock()
	c := make([]TestStatsdCall, len(tg.timingCalls))
	copy(c, tg.countCalls)
	return c
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

func (tg *TestStatsdClient) Counts() map[string]int64 {
	tg.mu.RLock()
	defer tg.mu.RUnlock()
	c := make(map[string]int64)
	for key, value := range tg.counts {
		c[key] = value
	}
	return c
}

func (tg *TestStatsdClient) Tags() []string {
	tg.mu.RLock()
	defer tg.mu.RUnlock()
	t := make([]string, len(tg.tags))
	copy(t, tg.tags)
	return t
}

func (tg *TestStatsdClient) Reset() {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	tg.gaugeCalls = tg.gaugeCalls[:0]
	tg.incrCalls = tg.incrCalls[:0]
	tg.countCalls = tg.countCalls[:0]
	tg.timingCalls = tg.timingCalls[:0]
	tg.counts = make(map[string]int64)
	tg.tags = tg.tags[:0]
	tg.n = 0
}

// Wait blocks until n metrics have been reported using the statsdtest.TestStatsdClient or until duration d passes.
// If d passes, or a wait is already active, an error is returned.
func (tg *TestStatsdClient) Wait(asserts *assert.Assertions, n int, d time.Duration) error {
	c := func() bool {
		tg.mu.RLock()
		defer tg.mu.RUnlock()

		return tg.n >= n
	}
	if !asserts.Eventually(c, d, 50*time.Millisecond) {
		return fmt.Errorf("timed out after waiting %s for gauge events", d)
	}

	return nil
}

func (tg *TestStatsdClient) Closed() bool {
	return tg.closed
}

func (tg *TestStatsdClient) Flushed() int {
	return tg.flushed
}
