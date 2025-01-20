// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import (
	"sync"
	"time"
)

type TickFunc func()

type Ticker struct {
	*time.Ticker

	tickSpeedMu sync.Mutex
	tickSpeed   time.Duration

	maxInterval time.Duration
	minInterval time.Duration

	tickFunc TickFunc
}

func NewTicker(tickFunc TickFunc, minInterval, maxInterval time.Duration) *Ticker {
	ticker := &Ticker{
		Ticker:    time.NewTicker(maxInterval),
		tickSpeed: maxInterval,

		maxInterval: maxInterval,
		minInterval: minInterval,

		tickFunc: tickFunc,
	}

	go func() {
		for range ticker.C {
			tickFunc()
		}
	}()

	return ticker
}

func (t *Ticker) IncreaseSpeed() {
	t.tickSpeedMu.Lock()
	defer t.tickSpeedMu.Unlock()

	t.tickSpeed = max(t.tickSpeed/2, t.minInterval)
	t.Reset(t.tickSpeed)
}

func (t *Ticker) DecreaseSpeed() {
	t.tickSpeedMu.Lock()
	defer t.tickSpeedMu.Unlock()

	t.tickSpeed = min(t.tickSpeed*2, t.maxInterval)
	t.Reset(t.tickSpeed)
}
