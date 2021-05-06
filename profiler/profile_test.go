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
	t.Run("delta", func(t *testing.T) {
		var (
			deltaPeriod = DefaultPeriod
			timeA       = time.Now().Truncate(time.Minute)
			timeB       = timeA.Add(deltaPeriod)
		)

		tests := []struct {
			Types        []ProfileType
			Prof1        textProfile
			Prof2        textProfile
			WantDelta    textProfile
			WantDuration time.Duration
		}{
			// For the mutex and block profile, we derive the delta for all sample
			// types, so we can test with a generic sample profile.
			{
				Types: []ProfileType{MutexProfile, BlockProfile},
				Prof1: textProfile{
					Time: timeA,
					Text: `
stuff/count
main 3
main;bar 2
main;foo 5
`,
				},
				Prof2: textProfile{
					Time: timeB,
					Text: `
stuff/count
main 4
main;bar 2
main;foo 8
main;foobar 7
`,
				},
				WantDelta: textProfile{
					Time: timeA,
					Text: `
stuff/count
main 1
main;foo 3
main;foobar 7
`,
				},
				WantDuration: deltaPeriod,
			},

			// For the heap profile, we must only derive deltas for the
			// alloc_objects/count and alloc_space/bytes sample type, so we use a
			// more realistic example and makes sure it is handled accurately.
			{
				Types: []ProfileType{HeapProfile},
				Prof1: textProfile{
					Time: timeA,
					Text: `
alloc_objects/count alloc_space/bytes inuse_objects/count inuse_space/bytes
main 3 6 12 24
main;bar 2 4 8 16
main;foo 5 10 20 40
`,
				},
				Prof2: textProfile{
					Time: timeB,
					Text: `
alloc_objects/count alloc_space/bytes inuse_objects/count inuse_space/bytes
main 4 8 16 32
main;bar 2 4 8 16
main;foo 8 16 32 64
main;foobar 7 14 28 56
`,
				},
				WantDelta: textProfile{
					Time: timeA,
					Text: `
alloc_objects/count alloc_space/bytes inuse_objects/count inuse_space/bytes
main 1 2 16 32
main;bar 0 0 8 16
main;foo 3 6 32 64
main;foobar 7 14 28 56
`,
				},
				WantDuration: deltaPeriod,
			},
		}

		for _, test := range tests {
			for _, profType := range test.Types {
				t.Run(profType.String(), func(t *testing.T) {
					prof1 := test.Prof1.Protobuf()
					prof2 := test.Prof2.Protobuf()

					returnProfs := [][]byte{prof1, prof2}
					defer func(old func(_ string, _ io.Writer, _ int) error) { lookupProfile = old }(lookupProfile)
					lookupProfile = func(name string, w io.Writer, _ int) error {
						_, err := w.Write(returnProfs[0])
						returnProfs = returnProfs[1:]
						return err
					}
					p, err := unstartedProfiler()

					// first run, should produce the current profile twice (a bit
					// awkward, but makes sense since we try to add delta profiles as an
					// additional profile type to ease the transition)
					profs, err := p.runProfile(profType)
					require.NoError(t, err)
					require.Equal(t, 2, len(profs))
					require.Equal(t, profType.Filename(), profs[0].name)
					require.Equal(t, prof1, profs[0].data)
					require.Equal(t, "delta-"+profType.Filename(), profs[1].name)
					require.Equal(t, prof1, profs[1].data)

					// second run, should produce p1 profile and delta profile
					profs, err = p.runProfile(profType)
					require.NoError(t, err)
					require.Equal(t, 2, len(profs))
					require.Equal(t, profType.Filename(), profs[0].name)
					require.Equal(t, prof2, profs[0].data)
					require.Equal(t, "delta-"+profType.Filename(), profs[1].name)
					require.Equal(t, test.WantDelta.String(), protobufToText(profs[1].data))

					// check delta prof details like timestamps and duration
					deltaProf, err := pprofile.ParseData(profs[1].data)
					require.NoError(t, err)
					require.Equal(t, test.Prof1.Time.UnixNano(), deltaProf.TimeNanos)
					require.Equal(t, deltaPeriod.Nanoseconds(), deltaProf.DurationNanos)
				})
			}
		}
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

// textProfile is a test helper for converting folded text to pprof protobuf
// profiles.
// See https://github.com/brendangregg/FlameGraph#2-fold-stacks
type textProfile struct {
	Text string
	Time time.Time
}

// Protobuf converts the profile to pprof's protobuf format or panics if there
// is an error.
func (t textProfile) Protobuf() []byte {
	out := &bytes.Buffer{}
	prof, err := pprofutils.Text{}.Convert(strings.NewReader(t.String()))
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

// String returns text without leading or trailing whitespace other than a
// trailing newline.
func (t textProfile) String() string {
	return strings.TrimSpace(t.Text) + "\n"
}

// protobufToText converts a protobuf pprof profile to text format or panics
// if there is an error.
func protobufToText(pprofData []byte) string {
	prof, err := pprofile.ParseData(pprofData)
	if err != nil {
		panic(err)
	}
	out := &bytes.Buffer{}
	if err := (pprofutils.Protobuf{SampleTypes: true}).Convert(prof, out); err != nil {
		panic(err)
	}
	return out.String()
}
