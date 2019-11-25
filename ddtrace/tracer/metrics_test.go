// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package tracer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestReportMetrics(t *testing.T) {
	var tg testStatsdClient
	trc := &tracer{
		stopped: make(chan struct{}),
		config: &config{
			statsd: &tg,
		},
	}

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
