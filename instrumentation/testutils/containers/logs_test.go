// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package containers

import (
	"testing"

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

// TestTestLogConsumerLateLog verifies that a log line delivered after the test
// and its cleanups have completed does not call t.Log. The container log
// producer goroutine can deliver one in-flight line once testcontainers-go has
// stopped the producer, and testing panics on Log after completion.
func TestTestLogConsumerLateLog(t *testing.T) {
	var consumer testcontainers.LogConsumer
	t.Run("inner", func(t *testing.T) {
		consumer = TestLogConsumer(t)
	})
	// The inner subtest has returned, so its cleanup ran and set the done flag.
	c := consumer.(testLogConsumer)
	if !c.done.Load() {
		t.Fatal("cleanup did not mark the consumer done")
	}
	// Any further line must be dropped rather than logged: finishedTB.Log
	// panics, and a panic here fails the test.
	c.TB = finishedTB{}
	c.Accept(testcontainers.Log{Content: []byte("late log line")})
}
