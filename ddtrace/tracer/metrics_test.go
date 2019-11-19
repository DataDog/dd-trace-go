// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package tracer

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestReportMetrics(t *testing.T) {
	trc := &tracer{
		stopped: make(chan struct{}),
	}

	var tg testGauger
	trc.statsd = &tg
	go trc.reportMetrics(time.Millisecond)
	err := tg.Wait(35, 1*time.Second)
	close(trc.stopped)
	assert := assert.New(t)
	assert.NoError(err)
	calls := tg.CallNames()
	assert.True(len(calls) > 30)
	assert.Contains(calls, "runtime.go.num_cpu")
	assert.Contains(calls, "runtime.go.mem_stats.alloc")
	assert.Contains(calls, "runtime.go.gc_stats.pause_quantiles.75p")
}

type testGauger struct {
	mu     sync.RWMutex
	calls  []string
	tags   []string
	waitCh chan struct{}
	n      int
}

func (tg *testGauger) Gauge(name string, value float64, tags []string, rate float64) error {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	tg.calls = append(tg.calls, name)
	tg.tags = tags
	if tg.n > 0 {
		tg.n--
		if tg.n == 0 {
			close(tg.waitCh)
		}
	}
	return nil
}

func (tg *testGauger) Incr(name string, tags []string, rate float64) error {
	return nil
}

func (tg *testGauger) Count(name string, value int64, tags []string, rate float64) error {
	return nil
}

func (tg *testGauger) Timing(name string, value time.Duration, tags []string, rate float64) error {
	return nil
}

func (tg *testGauger) Close() error {
	return nil
}

func (tg *testGauger) CallNames() []string {
	tg.mu.RLock()
	defer tg.mu.RUnlock()
	return tg.calls
}

func (tg *testGauger) Tags() []string {
	tg.mu.RLock()
	defer tg.mu.RUnlock()
	return tg.tags
}

func (tg *testGauger) Reset() {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	tg.calls = tg.calls[:0]
	tg.tags = tg.tags[:0]
	if tg.waitCh != nil {
		close(tg.waitCh)
		tg.waitCh = nil
	}
	tg.n = 0
}

// Wait blocks until n metrics have been reported using the testGauger or until duration d passes.
// If d passes, or a wait is already active, an error is returned.
func (tg *testGauger) Wait(n int, d time.Duration) error {
	tg.mu.Lock()
	if tg.waitCh != nil {
		tg.mu.Unlock()
		return errors.New("already waiting")
	}
	tg.waitCh = make(chan struct{})
	tg.n = n
	tg.mu.Unlock()

	select {
	case <-tg.waitCh:
		return nil
	case <-time.After(d):
		return fmt.Errorf("timed out after waiting %s for gauge events", d)
	}
}
