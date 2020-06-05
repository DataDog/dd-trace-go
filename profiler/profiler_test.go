// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package profiler

import (
	"io"
	"net"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStart(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		if err := Start(); err != nil {
			t.Fatal(err)
		}
		defer Stop()

		mu.Lock()
		require.NotNil(t, activeProfiler)
		assert := assert.New(t)
		if host, err := os.Hostname(); err != nil {
			assert.Equal(host, activeProfiler.cfg.hostname)
		}
		assert.Equal("http://"+net.JoinHostPort(defaultAgentHost, defaultAgentPort)+"/profiling/v1/input",
			activeProfiler.cfg.agentURL)
		assert.Equal(defaultAPIURL, activeProfiler.cfg.apiURL)
		assert.Equal(activeProfiler.cfg.agentURL, activeProfiler.cfg.targetURL)
		assert.Equal(DefaultPeriod, activeProfiler.cfg.period)
		assert.Equal(len(defaultProfileTypes), len(activeProfiler.cfg.types))
		for _, pt := range defaultProfileTypes {
			_, ok := activeProfiler.cfg.types[pt]
			assert.True(ok)
		}
		assert.Equal(DefaultDuration, activeProfiler.cfg.cpuDuration)
		mu.Unlock()
	})

	t.Run("options", func(t *testing.T) {
		if err := Start(); err != nil {
			t.Fatal(err)
		}
		defer Stop()

		mu.Lock()
		require.NotNil(t, activeProfiler)
		assert.NotEmpty(t, activeProfiler.cfg.hostname)
		mu.Unlock()
	})
}

func TestStartStopIdempotency(t *testing.T) {
	t.Run("linear", func(t *testing.T) {
		Start()
		Start()
		Start()
		Start()
		Start()
		Start()

		Stop()
		Stop()
		Stop()
		Stop()
		Stop()
	})

	t.Run("parallel", func(t *testing.T) {
		var wg sync.WaitGroup

		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 1000; j++ {
					Start()
				}
			}()
		}
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 1000; j++ {
					Stop()
				}
			}()
		}
		wg.Wait()
	})

	t.Run("stop", func(t *testing.T) {
		Start(WithPeriod(time.Minute))
		defer Stop()

		mu.Lock()
		require.NotNil(t, activeProfiler)
		activeProfiler.stop()
		activeProfiler.stop()
		activeProfiler.stop()
		activeProfiler.stop()
		mu.Unlock()
	})
}

func TestProfilerInternal(t *testing.T) {
	t.Run("collect", func(t *testing.T) {
		p, err := unstartedProfiler(
			CPUDuration(1*time.Millisecond),
			WithProfileTypes(HeapProfile, CPUProfile),
		)
		require.NoError(t, err)
		var startCPU, stopCPU, writeHeap uint64
		defer func(old func(_ io.Writer) error) { startCPUProfile = old }(startCPUProfile)
		startCPUProfile = func(_ io.Writer) error {
			atomic.AddUint64(&startCPU, 1)
			return nil
		}
		defer func(old func()) { stopCPUProfile = old }(stopCPUProfile)
		stopCPUProfile = func() { atomic.AddUint64(&stopCPU, 1) }
		defer func(old func(_ io.Writer) error) { writeHeapProfile = old }(writeHeapProfile)
		writeHeapProfile = func(_ io.Writer) error {
			atomic.AddUint64(&writeHeap, 1)
			return nil
		}
		tick := make(chan time.Time)
		wait := make(chan struct{})

		go func() {
			p.collect(tick)
			close(wait)
		}()

		tick <- time.Now()

		var bat batch
		select {
		case bat = <-p.out:
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("missing batch")
		}

		assert := assert.New(t)
		assert.EqualValues(1, writeHeap)
		assert.EqualValues(1, startCPU)
		assert.EqualValues(1, stopCPU)

		assert.Equal(2, len(bat.profiles))
		firstTypes := []string{
			bat.profiles[0].types[0],
			bat.profiles[1].types[0],
		}
		sort.Strings(firstTypes)
		assert.Equal("alloc_objects", firstTypes[0])
		assert.Equal("samples", firstTypes[1])

		p.exit <- struct{}{}
		<-wait
	})
}

func TestSetProfileFraction(t *testing.T) {
	t.Run("on", func(t *testing.T) {
		start := runtime.SetMutexProfileFraction(-1)
		defer runtime.SetMutexProfileFraction(start)
		p, err := unstartedProfiler(WithProfileTypes(MutexProfile))
		require.NoError(t, err)
		p.run()
		p.stop()
		assert.NotEqual(t, start, runtime.SetMutexProfileFraction(-1))
	})

	t.Run("off", func(t *testing.T) {
		start := runtime.SetMutexProfileFraction(-1)
		defer runtime.SetMutexProfileFraction(start)
		p, err := unstartedProfiler()
		require.NoError(t, err)
		p.run()
		p.stop()
		assert.Equal(t, start, runtime.SetMutexProfileFraction(-1))
	})
}

func TestProfilerPassthrough(t *testing.T) {
	if testing.Short() {
		return
	}
	out := make(chan batch)
	p, err := newProfiler()
	require.NoError(t, err)
	p.cfg.period = 200 * time.Millisecond
	p.cfg.cpuDuration = 1 * time.Millisecond
	p.uploadFunc = func(bat batch) error {
		out <- bat
		return nil
	}
	p.run()
	var bat batch
	select {
	case bat = <-out:
	// TODO (knusbaum) this timeout is long because we were seeing timeouts at 500ms.
	// it would be nice to have a time-independent way to test this
	case <-time.After(1000 * time.Millisecond):
		t.Fatal("time expired")
	}

	assert := assert.New(t)
	assert.Equal(2, len(bat.profiles))
	firstTypes := []string{
		bat.profiles[0].types[0],
		bat.profiles[1].types[0],
	}
	sort.Strings(firstTypes)
	assert.Equal("alloc_objects", firstTypes[0])
	assert.Equal("samples", firstTypes[1])
	assert.NotEmpty(bat.profiles[0].data)
	assert.NotEmpty(bat.profiles[1].data)
}

func unstartedProfiler(opts ...Option) (*profiler, error) {
	p, err := newProfiler(opts...)
	if err != nil {
		return nil, err
	}
	p.uploadFunc = func(_ batch) error { return nil }
	return p, nil
}
