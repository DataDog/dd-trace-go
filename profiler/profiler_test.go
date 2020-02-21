// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package profiler

import (
	"io"
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
	t.Run("api-key", func(t *testing.T) {
		require.Equal(t, ErrMissingAPIKey, Start())
	})

	t.Run("defaults", func(t *testing.T) {
		if err := Start(WithAPIKey("123")); err != nil {
			t.Fatal(err)
		}
		defer Stop()

		mu.Lock()
		require.NotNil(t, activeProfiler)
		if host, err := os.Hostname(); err != nil {
			assert.Equal(t, host, activeProfiler.cfg.hostname)
		}
		assert.Equal(t, defaultAPIURL, activeProfiler.cfg.apiURL)
		assert.Equal(t, DefaultPeriod, activeProfiler.cfg.period)
		assert.Equal(t, len(defaultProfileTypes), len(activeProfiler.cfg.types))
		for _, pt := range defaultProfileTypes {
			_, ok := activeProfiler.cfg.types[pt]
			assert.True(t, ok)
		}
		assert.Equal(t, DefaultDuration, activeProfiler.cfg.cpuDuration)
		mu.Unlock()
	})

	t.Run("options", func(t *testing.T) {
		if err := Start(WithAPIKey("123"), WithHostname("my-host")); err != nil {
			t.Fatal(err)
		}
		defer Stop()

		mu.Lock()
		require.NotNil(t, activeProfiler)
		assert.Equal(t, "my-host", activeProfiler.cfg.hostname)
		mu.Unlock()
	})
}

func TestStartStopIdempotency(t *testing.T) {
	t.Run("linear", func(t *testing.T) {
		Start(WithAPIKey("123"))
		Start(WithAPIKey("123"))
		Start(WithAPIKey("123"))
		Start(WithAPIKey("123"))
		Start(WithAPIKey("123"))
		Start(WithAPIKey("123"))

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
					Start(WithAPIKey("123"))
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
		Start(WithAPIKey("123"), WithPeriod(time.Minute))
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
		p := unstartedProfiler(
			CPUDuration(1*time.Millisecond),
			WithProfileTypes(HeapProfile, CPUProfile),
		)
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

		assert.EqualValues(t, 1, writeHeap)
		assert.EqualValues(t, 1, startCPU)
		assert.EqualValues(t, 1, stopCPU)

		assert.Equal(t, 2, len(bat.profiles))
		firstTypes := []string{
			bat.profiles[0].types[0],
			bat.profiles[1].types[0],
		}
		sort.Strings(firstTypes)
		assert.Equal(t, "alloc_objects", firstTypes[0])
		assert.Equal(t, "samples", firstTypes[1])

		p.exit <- struct{}{}
		<-wait
	})
}

func TestSetProfileFraction(t *testing.T) {
	t.Run("on", func(t *testing.T) {
		start := runtime.SetMutexProfileFraction(-1)
		defer runtime.SetMutexProfileFraction(start)
		p := unstartedProfiler(WithProfileTypes(MutexProfile))
		p.run()
		p.stop()
		assert.NotEqual(t, start, runtime.SetMutexProfileFraction(-1))
	})

	t.Run("off", func(t *testing.T) {
		start := runtime.SetMutexProfileFraction(-1)
		defer runtime.SetMutexProfileFraction(start)
		p := unstartedProfiler()
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
	cfg := defaultConfig()
	cfg.period = 200 * time.Millisecond
	cfg.cpuDuration = 1 * time.Millisecond
	p := newProfiler(cfg)
	p.uploadFunc = func(bat batch) error {
		out <- bat
		return nil
	}
	p.run()
	var bat batch
loop:
	for {
		select {
		case bat = <-p.out:
			break loop
		case <-time.After(500 * time.Millisecond):
			t.Fatal("time expired")
		}
	}

	assert.Equal(t, 2, len(bat.profiles))
	firstTypes := []string{
		bat.profiles[0].types[0],
		bat.profiles[1].types[0],
	}
	sort.Strings(firstTypes)
	assert.Equal(t, "alloc_objects", firstTypes[0])
	assert.Equal(t, "samples", firstTypes[1])
	assert.NotEmpty(t, bat.profiles[0].data)
	assert.NotEmpty(t, bat.profiles[1].data)
}

func unstartedProfiler(opts ...Option) *profiler {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	p := newProfiler(cfg)
	p.uploadFunc = func(_ batch) error { return nil }
	return p
}
