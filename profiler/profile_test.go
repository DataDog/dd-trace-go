// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strconv"
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

		requireFunctions := func(t *testing.T, s *pprofile.Sample, want []string) {
			t.Helper()
			var got []string
			for _, loc := range s.Location {
				got = append(got, loc.Line[0].Function.Name)
			}
			require.Equal(t, want, got)
		}

		pp, err := pprofile.Parse(bytes.NewReader(prof.data))
		require.NoError(t, err)
		// timestamp
		require.NotEqual(t, int64(0), pp.TimeNanos)
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
		requireFunctions(t, pp.Sample[2], []string{
			"main.stackDump",
			"main.main",
			"...additional frames elided...",
		})
		// Virtual frame go "created by" frame
		requireFunctions(t, pp.Sample[1], []string{
			"time.Sleep",
			"main.indirectShortSleepLoop2",
		})
	})

	t.Run("goroutineswaitLimit", func(t *testing.T) {
		// spawGoroutines spawns n goroutines, waits for them to start executing,
		// and then returns a func to stop them. For more details about `executing`
		// see:
		// https://github.com/DataDog/dd-trace-go/pull/942#discussion_r656924335
		spawnGoroutines := func(n int) func() {
			executing := make(chan struct{})
			stopping := make(chan struct{})
			for i := 0; i < n; i++ {
				go func() {
					executing <- struct{}{}
					stopping <- struct{}{}
				}()
				<-executing
			}
			return func() {
				for i := 0; i < n; i++ {
					<-stopping
				}
			}
		}

		goroutines := 100
		limit := 10

		stop := spawnGoroutines(goroutines)
		defer stop()
		envVar := "DD_PROFILING_WAIT_PROFILE_MAX_GOROUTINES"
		oldVal := os.Getenv(envVar)
		os.Setenv(envVar, strconv.Itoa(limit))
		defer os.Setenv(envVar, oldVal)

		defer func(old func(_ string, _ io.Writer, _ int) error) { lookupProfile = old }(lookupProfile)
		lookupProfile = func(_ string, w io.Writer, _ int) error {
			_, err := w.Write([]byte(""))
			return err
		}

		p, err := unstartedProfiler()
		require.NoError(t, err)
		_, err = p.runProfile(expGoroutineWaitProfile)
		var errRoutines, errLimit int
		msg := "skipping goroutines wait profile: %d goroutines exceeds DD_PROFILING_WAIT_PROFILE_MAX_GOROUTINES limit of %d"
		fmt.Sscanf(err.Error(), msg, &errRoutines, &errLimit)
		require.GreaterOrEqual(t, errRoutines, goroutines)
		require.Equal(t, limit, errLimit)
	})
}

func Test_goroutineDebug2ToPprof_CrashSafety(t *testing.T) {
	err := goroutineDebug2ToPprof(panicReader{}, ioutil.Discard, time.Time{})
	require.NotNil(t, err)
	require.Equal(t, "panic: 42", err.Error())
}

// panicReader is used to create a panic inside of stackparse.Parse()
type panicReader struct{}

func (c panicReader) Read(_ []byte) (int, error) {
	panic("42")
}
