// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestGLSFinishResetConcurrentRaces models a span that is finished on one
// goroutine while the tracer's sync.Pool recycles and resets that same span on
// another goroutine. GLSDeactivate is woven into span finish and reads then
// writes the injected *pop field, while GLSReset is woven into span clear and
// writes the same injected *pop field during pool reuse. Both operations share
// only the atomic reclaimable flag's ordering; the popper field itself is plain
// memory with no mutual synchronization. Therefore the orchestrion.yml
// "happens-before" guarantee for reclaimability does not cover the popper
// field, and go test -race should report an unsynchronized race between
// GLSDeactivate and GLSReset.
func TestGLSFinishResetConcurrentRaces(t *testing.T) {
	for range 1000 {
		var reclaimable atomic.Bool
		var pop GLSPopper = func() {}

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			<-start
			GLSDeactivate(&reclaimable, &pop)
		}()

		go func() {
			defer wg.Done()
			<-start
			GLSReset(&reclaimable, &pop)
		}()

		close(start)
		wg.Wait()
	}
}
