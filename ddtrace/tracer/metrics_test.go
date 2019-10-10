// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package tracer

import (
	"sync"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"

	"github.com/stretchr/testify/assert"
)

func TestReportMetrics(t *testing.T) {
	trc := &tracer{
		stopped: make(chan struct{}),
		config: &config{
			serviceName: "my-service",
			hostname:    "my-host",
			globalTags:  map[string]interface{}{ext.Environment: "my-env"},
		},
	}

	var tg testGauger
	ch := make(chan struct{}, 30)
	tg.ch = ch
	go trc.reportMetrics(&tg, time.Millisecond)
	for i := 0; i < 30; i++ {
		<-ch
	}
	close(trc.stopped)
	assert := assert.New(t)
	calls := tg.CallNames()
	tags := tg.Tags()
	assert.True(len(calls) > 30)
	assert.Contains(calls, "runtime.go.num_cpu")
	assert.Contains(calls, "runtime.go.mem_stats.alloc")
	assert.Contains(calls, "runtime.go.gc_stats.pause_quantiles.75p")
	assert.Contains(tags, "service:my-service")
	assert.Contains(tags, "env:my-env")
	assert.Contains(tags, "host:my-host")
}

type testGauger struct {
	mu    sync.RWMutex
	calls []string
	tags  []string
	ch    chan<- struct{}
}

func (tg *testGauger) Gauge(name string, value float64, tags []string, rate float64) error {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	tg.calls = append(tg.calls, name)
	tg.tags = tags
	select {
	case tg.ch <- struct{}{}:
	default:
	}
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
}
