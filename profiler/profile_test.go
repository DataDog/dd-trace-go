// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"bytes"
	"io"
	"io/ioutil"
	"testing"
	"time"

	pprofile "github.com/google/pprof/profile"
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
		defer func(old func(_ string, _ io.Writer, _ int) error) { lookupProfile = old }(lookupProfile)
		lookupProfile = func(name string, w io.Writer, _ int) error {
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
		defer func(old func(_ string, _ io.Writer, _ int) error) { lookupProfile = old }(lookupProfile)
		lookupProfile = func(name string, w io.Writer, _ int) error {
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
		defer func(old func(_ string, _ io.Writer, _ int) error) { lookupProfile = old }(lookupProfile)
		lookupProfile = func(name string, w io.Writer, _ int) error {
			_, err := w.Write([]byte(name))
			return err
		}

		p, err := unstartedProfiler()
		prof, err := p.runProfile(GoroutineProfile)
		require.NoError(t, err)
		assert.Equal(t, "goroutines.pprof", prof.name)
		assert.Equal(t, []byte("goroutine"), prof.data)
	})

	t.Run("goroutinewait", func(t *testing.T) {
		const sample = `
goroutine 1 [running]:
main.main()
	/example/main.go:152 +0x3d2

goroutine 2 [running]:
badFunctionCall)(

goroutine 3 [sleep, 1 minutes]:
time.Sleep(0x3b9aca00)
	/usr/local/Cellar/go/1.15.6/libexec/src/runtime/time.go:188 +0xbf
created by main.indirectShortSleepLoop2
	/example/main.go:185 +0x35

goroutine 4 [running]:
main.stackDump(0x62)
	/example/max_frames.go:20 +0x131
main.main()
	/example/max_frames.go:11 +0x2a
...additional frames elided...
`

		defer func(old func(_ string, _ io.Writer, _ int) error) { lookupProfile = old }(lookupProfile)
		lookupProfile = func(name string, w io.Writer, _ int) error {
			_, err := w.Write([]byte(sample))
			return err
		}

		p, err := unstartedProfiler()
		prof, err := p.runProfile(expGoroutineWaitProfile)
		require.NoError(t, err)
		require.Equal(t, "goroutineswait.pprof", prof.name)

		// pro tip: enable line below to inspect the pprof output using cli tools
		// ioutil.WriteFile(prof.name, prof.data, 0644)

		pp, err := pprofile.Parse(bytes.NewReader(prof.data))
		require.NoError(t, err)
		// 1 sample type
		require.Equal(t, 1, len(pp.SampleType))
		// 3 valid samples, 1 invalid sample (added as comment)
		require.Equal(t, 3, len(pp.Sample))
		require.Equal(t, 1, len(pp.Comments))
		// Wait duration
		require.Equal(t, []int64{time.Minute.Nanoseconds()}, pp.Sample[1].Value)
		// Labels
		require.Equal(t, []string{"running"}, pp.Sample[0].Label["state"])
		require.Equal(t, []string{"false"}, pp.Sample[0].Label["lockedm"])
		require.Equal(t, []int64{3}, pp.Sample[1].NumLabel["goid"])
		require.Equal(t, []string{"id"}, pp.Sample[1].NumUnit["goid"])
		// Virtual frame for "frames elided" goroutine
		require.Equal(t, 3, len(pp.Sample[2].Location))
		// Virtual frame go "created by" frame
		require.Equal(t, 2, len(pp.Sample[1].Location))
	})
}

func Test_goroutineDebug2ToPprof_CrashSafety(t *testing.T) {
	err := goroutineDebug2ToPprof(panicReader{}, ioutil.Discard)
	require.NotNil(t, err)
	require.Equal(t, "panic: 42", err.Error())
}

// panicReader is used to create a panic inside of stackparse.Parse()
type panicReader struct{}

func (c panicReader) Read(_ []byte) (int, error) {
	panic("42")
}
