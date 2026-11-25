// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package containers

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/testcontainers/testcontainers-go"
)

func WithTestLogConsumer(t testing.TB) testcontainers.CustomizeRequestOption {
	return testcontainers.WithLogConsumers(TestLogConsumer(t))
}

type testLogConsumer struct {
	testing.TB

	// mu guards done and serializes logging against the test cleanup, so the
	// cleanup cannot mark the consumer done while an admitted t.Log call is
	// still executing.
	mu   sync.Mutex
	done bool
}

func TestLogConsumer(t testing.TB) testcontainers.LogConsumer {
	c := &testLogConsumer{TB: t}
	// Cleanups run in LIFO order, and the consumer is created before
	// RegisterContainerCleanup registers its StopLogProducer cleanup, so this
	// runs last. Correctness does not depend on that ordering: markDone blocks
	// until any in-flight log returns.
	t.Cleanup(c.markDone)
	return c
}

func (c *testLogConsumer) markDone() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.done = true
}

func (c *testLogConsumer) Accept(log testcontainers.Log) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		// The container log producer goroutine can deliver a line after the
		// test has completed, and t.Log panics then. Drop it to stderr
		// instead, so late lines are not lost. End it with a newline: otherwise the
		// next "--- PASS" line is glued to it, test2json misses the result, and
		// gotestsum reports the test as failed.
		os.Stderr.WriteString("container log after test completion: " + strings.TrimSuffix(string(log.Content), "\n") + "\n")
		return
	}
	c.TB.Log(string(log.Content))
}
