// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.
package contribroutines

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestContribRoutines(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	var done bool
	go func() {
		doSomething(&wg, &done, GetStopChan())
	}()
	Stop()
	wg.Wait()
	assert.True(t, done)
}

func doSomething(wg *sync.WaitGroup, done *bool, stop chan struct{}) {
	defer wg.Done()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
		case <-stop:
			*done = true
			return
		}
	}
}

func TestStopConcurrency(t *testing.T) {
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Stop()
		}()
	}

	wg.Wait()

	select {
	case <-stop:
		// channel is closed, so Stop() was called successfully
	case <-time.After(1 * time.Second):
		t.Error("stop channel was not closed within 1 second")
	}
}

func TestGetStopChanConcurrency(t *testing.T) {
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			GetStopChan()
		}()
	}

	wg.Wait()
}
