// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"bytes"
	"io"
	"io/ioutil"
	"strings"
	"testing"
	"time"

	"github.com/felixge/pprofutils"
	pprofile "github.com/google/pprof/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunProfile(t *testing.T) {
	// p0 and p1 are generic dummy profiles that produce delta when diffed.
	var (
		p0 = textProfile{
			Time: time.Now().Truncate(time.Minute),
			Text: `
main 3
main;bar 2
main;foo 5
		`}
		p0Data = p0.Bytes()
		p1     = textProfile{
			Time: p0.Time.Add(5 * time.Minute),
			Text: `
main 4
main;bar 2
main;foo 8
main;foobar 7
		`}
		p1Data = p1.Bytes()
		delta  = strings.TrimSpace(`
main 1
main;foo 3
main;foobar 7
	`) + "\n"
	)

	// All delta-capable profile work the same, so we can test them with this
	// generic test inside of the loop.
	for _, profType := range []ProfileType{HeapProfile, MutexProfile, BlockProfile} {
		t.Run(profType.String(), func(t *testing.T) {
			returnProfs := [][]byte{p0Data, p1Data}
			defer func(old func(_ string, _ io.Writer, _ int) error) { lookupProfile = old }(lookupProfile)
			lookupProfile = func(name string, w io.Writer, _ int) error {
				_, err := w.Write(returnProfs[0])
				returnProfs = returnProfs[1:]
				return err
			}
			p, err := unstartedProfiler()

			// first run, should produce p0 profile
			profs, err := p.runProfile(profType)
			require.NoError(t, err)
			require.Equal(t, 1, len(profs))
			require.Equal(t, profType.Filename(), profs[0].name)
			require.Equal(t, p0Data, profs[0].data)

			// second run, should produce p1 profile and delta profile
			profs, err = p.runProfile(profType)
			require.NoError(t, err)
			require.Equal(t, 2, len(profs))
			require.Equal(t, profType.Filename(), profs[0].name)
			require.Equal(t, p1Data, profs[0].data)
			require.Equal(t, "delta-"+profType.Filename(), profs[1].name)
			require.Equal(t, delta, protobufToText(t, profs[1].data))

			// check delta prof details like timestamps and duration
			deltaProf, err := pprofile.ParseData(profs[1].data)
			require.NoError(t, err)
			require.Equal(t, p0.Time.UnixNano(), deltaProf.TimeNanos)
			require.Equal(t, p1.Time.Sub(p0.Time).Nanoseconds(), deltaProf.DurationNanos)
		})
	}

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
		profs, err := p.runProfile(CPUProfile)
		end := time.Now()
		require.NoError(t, err)
		assert.True(t, end.Sub(start) > 10*time.Millisecond)
		assert.Equal(t, "cpu.pprof", profs[0].name)
		assert.Equal(t, []byte("my-cpu-profile"), profs[0].data)
	})

	t.Run("goroutine", func(t *testing.T) {
		defer func(old func(_ string, _ io.Writer, _ int) error) { lookupProfile = old }(lookupProfile)
		lookupProfile = func(name string, w io.Writer, _ int) error {
			_, err := w.Write([]byte(name))
			return err
		}

		p, err := unstartedProfiler()
		profs, err := p.runProfile(GoroutineProfile)
		require.NoError(t, err)
		assert.Equal(t, "goroutines.pprof", profs[0].name)
		assert.Equal(t, []byte("goroutine"), profs[0].data)
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
		profs, err := p.runProfile(expGoroutineWaitProfile)
		require.NoError(t, err)
		require.Equal(t, "goroutineswait.pprof", profs[0].name)

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

		pp, err := pprofile.Parse(bytes.NewReader(profs[0].data))
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

type textProfile struct {
	Text string
	Time time.Time
}

func (t textProfile) Bytes() []byte {
	out := &bytes.Buffer{}
	prof, err := pprofutils.Text{}.Convert(strings.NewReader(t.Text))
	if err != nil {
		panic(err)
	}
	if !t.Time.IsZero() {
		prof.TimeNanos = t.Time.UnixNano()
	}
	if err := prof.Write(out); err != nil {
		panic(err)
	}
	return out.Bytes()
}

// protobufToText is a test helper that converts the binary protobuf profile
// into a folded profile string.
// See https://github.com/brendangregg/FlameGraph#2-fold-stacks
func protobufToText(t *testing.T, pprofData []byte) string {
	t.Helper()
	prof, err := pprofile.ParseData(pprofData)
	require.NoError(t, err)
	out := &bytes.Buffer{}
	err = pprofutils.Protobuf{}.Convert(prof, out)
	require.NoError(t, err)
	return out.String()
}
