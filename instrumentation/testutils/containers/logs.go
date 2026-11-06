// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package containers

import (
	"sync"
	"testing"

	"github.com/testcontainers/testcontainers-go"
)

func WithTestLogConsumer(t testing.TB) testcontainers.CustomizeRequestOption {
	return testcontainers.WithLogConsumers(TestLogConsumer(t))
}

// testLogConsumer forwards container log lines to t.Log from a single pump
// goroutine. testcontainers calls Accept from its own log-production
// goroutine, which can still be running after the test has completed;
// calling t.Log from there panics the whole test binary ("Log in goroutine
// after <test> has completed"). The pump is the only caller of t.Log, and a
// cleanup registered with the consumer stops and waits for it, so no log
// line can reach t after the test is done.
type testLogConsumer struct {
	t    testing.TB
	logs chan string
	// done is closed once by the cleanup to stop the pump.
	done     chan struct{}
	stopOnce sync.Once
	// stopped is closed by the pump when it returns.
	stopped chan struct{}
}

func TestLogConsumer(t testing.TB) testcontainers.LogConsumer {
	c := &testLogConsumer{
		t:       t,
		logs:    make(chan string, 1024),
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	t.Cleanup(func() {
		c.stopOnce.Do(func() { close(c.done) })
		<-c.stopped
	})
	go c.pump()
	return c
}

func (c *testLogConsumer) Accept(log testcontainers.Log) {
	select {
	case c.logs <- string(log.Content):
	case <-c.done:
		// The test is over; the line can no longer be logged safely.
	}
}

func (c *testLogConsumer) pump() {
	defer close(c.stopped)
	for {
		select {
		case line := <-c.logs:
			c.t.Log(line)
		case <-c.done:
			// Drain lines already delivered; the cleanup is still
			// running, so t.Log remains safe.
			for {
				select {
				case line := <-c.logs:
					c.t.Log(line)
				default:
					return
				}
			}
		}
	}
}
