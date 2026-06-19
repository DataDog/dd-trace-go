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

// TestGLSDeactivateConcurrentFinishRaces is expected to be flagged by
// `go test -race` as a data race on the GLSPopper field. It mirrors two
// concurrent dyngo FinishOperation calls that both run under the shared RLock
// and therefore can call GLSDeactivate on the same popper field at the same
// time.
func TestGLSDeactivateConcurrentFinishRaces(t *testing.T) {
	const glsDeactivateRaceIterations = 1000

	for range glsDeactivateRaceIterations {
		var glsDeactivateRaceReclaimable atomic.Bool
		var glsDeactivateRacePop GLSPopper = func() {}

		glsDeactivateRaceStart := make(chan struct{})
		var glsDeactivateRaceWG sync.WaitGroup
		glsDeactivateRaceWG.Add(2)

		for range 2 {
			go func() {
				defer glsDeactivateRaceWG.Done()
				<-glsDeactivateRaceStart
				GLSDeactivate(&glsDeactivateRaceReclaimable, &glsDeactivateRacePop)
			}()
		}

		close(glsDeactivateRaceStart)
		glsDeactivateRaceWG.Wait()
	}
}

// TestGLSDeactivateConcurrentDoubleRunsPopper documents the observable
// consequence of the non-atomic popper swap: two concurrent deactivations can
// both observe the same non-nil popper and run it. This assertion is inherently
// scheduling-dependent under plain go test; the reliable red signal for this
// bug is TestGLSDeactivateConcurrentFinishRaces under `go test -race`.
func TestGLSDeactivateConcurrentDoubleRunsPopper(t *testing.T) {
	const glsDeactivateDoubleRunIterations = 1000

	var glsDeactivateDoubleRunTotal atomic.Int64
	for range glsDeactivateDoubleRunIterations {
		var glsDeactivateDoubleRunReclaimable atomic.Bool
		var glsDeactivateDoubleRunPop GLSPopper = func() {
			glsDeactivateDoubleRunTotal.Add(1)
		}

		glsDeactivateDoubleRunStart := make(chan struct{})
		var glsDeactivateDoubleRunWG sync.WaitGroup
		glsDeactivateDoubleRunWG.Add(2)

		for range 2 {
			go func() {
				defer glsDeactivateDoubleRunWG.Done()
				<-glsDeactivateDoubleRunStart
				GLSDeactivate(&glsDeactivateDoubleRunReclaimable, &glsDeactivateDoubleRunPop)
			}()
		}

		close(glsDeactivateDoubleRunStart)
		glsDeactivateDoubleRunWG.Wait()
	}

	if got := glsDeactivateDoubleRunTotal.Load(); got != glsDeactivateDoubleRunIterations {
		t.Fatalf("GLSDeactivate ran popper %d times across %d concurrent deactivations; want exactly once per iteration", got, glsDeactivateDoubleRunIterations)
	}
}
