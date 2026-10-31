// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package internal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestSpeedChangesAfterStopAreIgnored verifies that speed changes after Stop
// leave the ticker stopped. A speed change calls Reset on the runtime ticker,
// which restarts its wakeups even after Stop. The worker already exited, so
// the restarted ticker would wake nobody and would run as long as the stopped
// Ticker stays referenced.
func TestSpeedChangesAfterStopAreIgnored(t *testing.T) {
	// The interval range must allow real speed changes: the initial speed is
	// Max, and halving it stays within the range.
	ticker := NewTicker(func() {}, Range[time.Duration]{Min: time.Second, Max: time.Hour})

	ticker.Stop()
	ticker.CanIncreaseSpeed()

	// The speed change after Stop must not touch the tick speed or the
	// runtime ticker, so the interval stays at its initial value. Without the
	// stop guard, this call would halve the interval to thirty minutes and
	// call Reset on the stopped runtime ticker.
	assert.Equal(t, time.Hour, ticker.tickSpeed)

	// The worker exits on the closed stop channel.
	done := ticker.Done()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the ticker worker did not exit after Stop")
	}
}
