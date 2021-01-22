// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunProfile(t *testing.T) {
	t.Run("heap", func(t *testing.T) {
		defer func(old func(_ io.Writer) error) { writeHeapProfile = old }(writeHeapProfile)
		writeHeapProfile = func(w io.Writer) error {
			_, err := w.Write([]byte("my-heap-profile"))
			return err
		}
		p, err := unstartedProfiler()
		prof, err := p.runProfile(HeapProfile)
		require.NoError(t, err)
		assert.Equal(t, "heap.pprof", prof.name)
		assert.Equal(t, []byte("my-heap-profile"), prof.data)
	})

	t.Run("cpu", func(t *testing.T) {
		defer func(old func(_ io.Writer) error) { startCPUProfile = old }(startCPUProfile)
		startCPUProfile = func(w io.Writer) error {
			_, err := w.Write([]byte("my-cpu-profile"))
			return err
		}
		defer func(old func()) { stopCPUProfile = old }(stopCPUProfile)
		stopCPUProfile = func() {}

		p, err := unstartedProfiler(CPUDuration(10 * time.Millisecond))
		start := time.Now()
		prof, err := p.runProfile(CPUProfile)
		end := time.Now()
		require.NoError(t, err)
		assert.True(t, end.Sub(start) > 10*time.Millisecond)
		assert.Equal(t, "cpu.pprof", prof.name)
		assert.Equal(t, []byte("my-cpu-profile"), prof.data)
	})

	t.Run("mutex", func(t *testing.T) {
		defer func(old func(_ string, _ io.Writer) error) { lookupProfile = old }(lookupProfile)
		lookupProfile = func(name string, w io.Writer) error {
			_, err := w.Write([]byte(name))
			return err
		}

		p, err := unstartedProfiler()
		prof, err := p.runProfile(MutexProfile)
		require.NoError(t, err)
		assert.Equal(t, "mutex.pprof", prof.name)
		assert.Equal(t, []byte("mutex"), prof.data)
	})

	t.Run("block", func(t *testing.T) {
		defer func(old func(_ string, _ io.Writer) error) { lookupProfile = old }(lookupProfile)
		lookupProfile = func(name string, w io.Writer) error {
			_, err := w.Write([]byte(name))
			return err
		}

		p, err := unstartedProfiler()
		prof, err := p.runProfile(BlockProfile)
		require.NoError(t, err)
		assert.Equal(t, "block.pprof", prof.name)
		assert.Equal(t, []byte("block"), prof.data)
	})

	t.Run("goroutine", func(t *testing.T) {
		defer func(old func(_ string, _ io.Writer) error) { lookupProfile = old }(lookupProfile)
		lookupProfile = func(name string, w io.Writer) error {
			_, err := w.Write([]byte(name))
			return err
		}

		p, err := unstartedProfiler()
		prof, err := p.runProfile(GoroutineProfile)
		require.NoError(t, err)
		assert.Equal(t, "goroutines.pprof", prof.name)
		assert.Equal(t, []byte("goroutine"), prof.data)
	})
}
