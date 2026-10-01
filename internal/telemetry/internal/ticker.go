// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import (
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

type TickFunc func()

type Ticker struct {
	ticker *time.Ticker

	tickSpeedMu sync.Mutex
	tickSpeed   time.Duration

	interval Range[time.Duration]

	tickFunc TickFunc

	stopOnce sync.Once
	stop     chan struct{} // closed by Stop; the worker exits on a closed stop
	done     chan struct{} // closed by the worker when it returns
}

func NewTicker(tickFunc TickFunc, interval Range[time.Duration]) *Ticker {
	ticker := &Ticker{
		ticker:    time.NewTicker(interval.Max),
		tickSpeed: interval.Max,
		interval:  interval,
		tickFunc:  tickFunc,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}

	go func() {
		defer close(ticker.done)
		for {
			select {
			case <-ticker.ticker.C:
				tickFunc()
			case <-ticker.stop:
				return
			}
		}
	}()

	return ticker
}

func (t *Ticker) CanIncreaseSpeed() {
	t.tickSpeedMu.Lock()
	defer t.tickSpeedMu.Unlock()

	oldTickSpeed := t.tickSpeed
	t.tickSpeed = t.interval.Clamp(t.tickSpeed / 2)

	if oldTickSpeed == t.tickSpeed {
		return
	}

	log.Debug("telemetry: increasing flush speed to an interval of %s", t.tickSpeed)
	t.ticker.Reset(t.tickSpeed)
}

func (t *Ticker) CanDecreaseSpeed() {
	t.tickSpeedMu.Lock()
	defer t.tickSpeedMu.Unlock()

	oldTickSpeed := t.tickSpeed
	t.tickSpeed = t.interval.Clamp(t.tickSpeed * 2)

	if oldTickSpeed == t.tickSpeed {
		return
	}

	log.Debug("telemetry: decreasing flush speed to an interval of %s", t.tickSpeed)
	t.ticker.Reset(t.tickSpeed)
}

// Stop stops the ticker. Stop is safe for concurrent use and never
// blocks: it only closes the stop channel, so a caller inside the worker's
// own tickFunc can stop the ticker without waiting for itself. The worker
// finishes the current tick before it exits. Wait for the worker through
// Done.
func (t *Ticker) Stop() {
	t.stopOnce.Do(func() {
		t.ticker.Stop()
		close(t.stop)
	})
}

// Done closes when the worker goroutine returned. The worker returns after
// Stop ran and the current tick ended.
func (t *Ticker) Done() <-chan struct{} {
	return t.done
}
