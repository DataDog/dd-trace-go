// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Go 1.27 removed the asynctimerchan setting. Before Go 1.27, a program can
// still use asynchronous timer channels, for example when its main module
// declares a Go version before 1.23. This file runs the tests of this package
// in that mode.

//go:build !go1.27

//go:debug asynctimerchan=1

package fasthttp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// waitQueuedTick waits until the expired timer has put its tick in its
// channel. With asynchronous timer channels, the tick stays there until a
// receive. With synchronous channels, len(timer.C) is always 0, so the tests in
// this file then fail: without a queued tick, they do not test the drain.
func waitQueuedTick(t *testing.T, timer *time.Timer) {
	t.Helper()
	require.Eventually(t, func() bool { return len(timer.C) == 1 }, 5*time.Second, time.Millisecond,
		"no tick is queued; timer channels are synchronous, or the //go:debug directive has no effect")
}

// requireNoTick makes sure that timer does not deliver a value in the next
// 20 ms.
func requireNoTick(t *testing.T, timer *time.Timer) {
	t.Helper()
	select {
	case <-timer.C:
		t.Fatal("the timer delivered the tick of an earlier expiry")
	case <-time.After(20 * time.Millisecond):
	}
	// Both cases can be ready at the same time. Then select can choose either.
	require.Zero(t, len(timer.C), "the timer delivered the tick of an earlier expiry")
}

// An expired timer keeps its value in an asynchronous channel. A reset must
// remove that value, or the new deadline expires at once.
func TestResetTimerDropsExpiredTick(t *testing.T) {
	timer := time.NewTimer(time.Millisecond)
	defer timer.Stop()
	waitQueuedTick(t, timer)
	resetTimer(timer, time.Hour)
	requireNoTick(t, timer)
}

// The inner deadline can expire just before the owner removes it. Its tick then
// stays in the timer channel. After the removal, the outer deadline must not
// expire at once.
func TestTimeoutRemovedDeadlineExpiredBeforeRemoval(t *testing.T) {
	outer := &timeoutDeadline{at: time.Now().Add(time.Hour), message: "outer timeout"}
	inner := &timeoutDeadline{at: time.Now().Add(time.Millisecond), message: "inner timeout"}
	layer := &timeoutLayer{deadlines: []*timeoutDeadline{outer, inner}}
	timer := time.NewTimer(time.Until(inner.at))
	defer timer.Stop()
	// The inner deadline expires. Nothing receives its tick.
	waitQueuedTick(t, timer)
	layer.updateDeadline(timer, timeoutEvent{deadline: inner})
	require.Equal(t, []*timeoutDeadline{outer}, layer.deadlines)
	requireNoTick(t, timer)
}
