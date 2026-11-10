// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package containers

import (
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

// finishedTB stands in for a test whose log destination is gone. Log on it
// panics, like testing.T does once the test has fully completed.
type finishedTB struct {
	testing.TB
}

func (finishedTB) Log(args ...any) {
	panic("Log in goroutine after Test has completed")
}

// blockingTB blocks inside Log until released, to pin a log call that is
// executing while the test cleanup runs.
type blockingTB struct {
	testing.TB
	started chan struct{}
	release chan struct{}
}

func (b blockingTB) Log(args ...any) {
	b.started <- struct{}{}
	<-b.release
}

// TestTestLogConsumerLateLog verifies that a log line delivered after the test
// and its cleanups have completed does not call t.Log. The container log
// producer goroutine can deliver one in-flight line once testcontainers-go has
// stopped the producer, and testing panics on Log after completion.
func TestTestLogConsumerLateLog(t *testing.T) {
	var consumer testcontainers.LogConsumer
	t.Run("inner", func(t *testing.T) {
		consumer = TestLogConsumer(t)
	})
	// The inner subtest has returned, so its cleanup ran and marked the
	// consumer done.
	c := consumer.(*testLogConsumer)
	if !c.done {
		t.Fatal("cleanup did not mark the consumer done")
	}
	// Any further line must be dropped rather than logged: finishedTB.Log
	// panics, and a panic here fails the test.
	c.TB = finishedTB{}
	c.Accept(testcontainers.Log{Content: []byte("late log line")})
}

// TestTestLogConsumerConcurrentTeardown verifies that the cleanup does not
// mark the consumer done while an admitted t.Log call is still executing.
// Otherwise the test could complete, and the in-flight call would panic.
func TestTestLogConsumerConcurrentTeardown(t *testing.T) {
	tb := blockingTB{started: make(chan struct{}), release: make(chan struct{})}
	c := &testLogConsumer{TB: tb}

	// Admit a line; it blocks inside t.Log.
	go c.Accept(testcontainers.Log{Content: []byte("in-flight line")})
	<-tb.started

	// markDone is the function the test cleanup runs. While the admitted
	// t.Log call is executing it must block, not mark done and return.
	cleanupDone := make(chan struct{})
	go func() {
		c.markDone()
		close(cleanupDone)
	}()

	select {
	case <-cleanupDone:
		t.Fatal("cleanup finished while an admitted t.Log call was still executing")
	case <-time.After(100 * time.Millisecond):
	}

	// Once the admitted call returns, the cleanup must finish.
	close(tb.release)
	select {
	case <-cleanupDone:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not finish after the admitted t.Log call returned")
	}
}
