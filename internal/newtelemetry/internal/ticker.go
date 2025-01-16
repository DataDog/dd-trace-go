// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import (
	"sync"
	"time"
)

type flusher interface {
	Flush() (int, error)
}

type Ticker struct {
	*time.Ticker

	tickSpeedMu sync.Mutex
	tickSpeed   time.Duration

	maxInterval time.Duration
	minInterval time.Duration

	flusher flusher
}

func NewTicker(flusher flusher, minInterval, maxInterval time.Duration) *Ticker {
	ticker := &Ticker{
		Ticker:    time.NewTicker(maxInterval),
		tickSpeed: maxInterval,

		maxInterval: maxInterval,
		minInterval: minInterval,

		flusher: flusher,
	}

	go func() {
		for range ticker.C {
			_, err := flusher.Flush()
			if err != nil {
				// Reset the interval to the maximum value
				ticker.AdjustTickSpeed(maxInterval)
				return
			}
		}
	}()

	return ticker
}

func (t *Ticker) AdjustTickSpeed(interval time.Duration) {
	t.tickSpeedMu.Lock()
	defer t.tickSpeedMu.Unlock()

	t.tickSpeed = interval
	t.Reset(interval)
}
