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
	"strings"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/profiler/internal/pprofutils"

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
main;foobar 7
main;foo 3
main 1
`,
				},
				WantDuration: deltaPeriod,
			},

			// For the heap profile, we must only derive deltas for the
			// alloc_objects/count and alloc_space/bytes sample type, so we use a
			// more realistic example and make sure it is handled accurately.
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
main;foobar 7 14 28 56
main;foo 3 6 32 64
main 1 2 16 32
main;bar 0 0 8 16
`,
				},
				WantDuration: deltaPeriod,
			},
		}

		for _, test := range tests {
			for _, profType := range test.Types {
				// deltaProfiler returns an unstarted profiler that is fed prof1
				// followed by prof2 when calling runProfile().
				deltaProfiler := func(prof1, prof2 []byte, opts ...Option) (*profiler, func()) {
					returnProfs := [][]byte{prof1, prof2}
					opts = append(opts, WithPeriod(5*time.Millisecond))
					p, err := unstartedProfiler(opts...)
					p.testHooks.lookupProfile = func(_ string, w io.Writer, _ int) error {
						_, err := w.Write(returnProfs[0])
						returnProfs = returnProfs[1:]
						return err
					}
					require.NoError(t, err)
					return p, func() {}
				}

				t.Run(profType.String(), func(t *testing.T) {
					t.Run("enabled", func(t *testing.T) {
						prof1 := test.Prof1.Protobuf()
						prof2 := test.Prof2.Protobuf()
						p, cleanup := deltaProfiler(prof1, prof2)
						defer cleanup()
						// first run, should produce the current profile twice (a bit
						// awkward, but makes sense since we try to add delta profiles as an
						// additional profile type to ease the transition)
						profs, err := p.runProfile(profType)
						require.NoError(t, err)
						require.Equal(t, 1, len(profs))
						require.Equal(t, "delta-"+profType.Filename(), profs[0].name)
						require.Equal(t, prof1, profs[0].data)

						// second run, should produce p1 profile and delta profile
						profs, err = p.runProfile(profType)
						require.NoError(t, err)
						require.Equal(t, 1, len(profs))
						require.Equal(t, "delta-"+profType.Filename(), profs[0].name)
						require.Equal(t, test.WantDelta.String(), protobufToText(profs[0].data))

						// check delta prof details like timestamps and duration
						deltaProf, err := pprofile.ParseData(profs[0].data)
						require.NoError(t, err)
						require.Equal(t, test.Prof2.Time.UnixNano(), deltaProf.TimeNanos)
						require.Equal(t, deltaPeriod.Nanoseconds(), deltaProf.DurationNanos)
					})

					t.Run("disabled", func(t *testing.T) {
						prof1 := test.Prof1.Protobuf()
						prof2 := test.Prof2.Protobuf()
						p, cleanup := deltaProfiler(prof1, prof2, WithDeltaProfiles(false))
						defer cleanup()

						profs, err := p.runProfile(profType)
						require.NoError(t, err)
						require.Equal(t, 1, len(profs))
					})
				})
			}
		}
	})

	t.Run("cpu", func(t *testing.T) {
		p, err := unstartedProfiler(CPUDuration(10 * time.Millisecond))
		p.testHooks.startCPUProfile = func(w io.Writer) error {
			_, err := w.Write([]byte("my-cpu-profile"))
			return err
		}
		p.testHooks.stopCPUProfile = func() {}
		require.NoError(t, err)
		start := time.Now()
		profs, err := p.runProfile(CPUProfile)
		end := time.Now()
		require.NoError(t, err)
		assert.True(t, end.Sub(start) > 10*time.Millisecond)
		assert.Equal(t, "cpu.pprof", profs[0].name)
		assert.Equal(t, []byte("my-cpu-profile"), profs[0].data)
	})

	t.Run("goroutine", func(t *testing.T) {
		p, err := unstartedProfiler(WithPeriod(time.Millisecond))
		p.testHooks.lookupProfile = func(name string, w io.Writer, _ int) error {
			_, err := w.Write([]byte(name))
			return err
		}
		require.NoError(t, err)
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

		p, err := unstartedProfiler()
		p.testHooks.lookupProfile = func(_ string, w io.Writer, _ int) error {
			_, err := w.Write([]byte(sample))
			return err
		}

		require.NoError(t, err)
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

		p, err := unstartedProfiler()
		p.testHooks.lookupProfile = func(_ string, w io.Writer, _ int) error {
			_, err := w.Write([]byte(""))
			return err
		}
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
	for _, st := range prof.SampleType {
		if st.Type == "alloc_space" {
			// this is a heap profile, add the correct period type
			// to make pprofile.Merge happy since the C allocation
			// profiler assumes it's generating a profile to merge
			// with the real heap profile.
			prof.PeriodType = &pprofile.ValueType{Type: "space", Unit: "bytes"}
			break
		}
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

// protobufToText is a test helper that converts a protobuf pprof profile to
// text format or panics if there is an error.
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

// TestProfileTypeSoundness fails if somebody tries to add a new profile type
// without adding it to enabledProfileTypes as well.
func TestProfileTypeSoundness(t *testing.T) {
	t.Run("enabledProfileTypes", func(t *testing.T) {
		var allProfileTypes []ProfileType
		for pt := range profileTypes {
			allProfileTypes = append(allProfileTypes, pt)
		}
		p, err := unstartedProfiler(WithProfileTypes(allProfileTypes...))
		require.NoError(t, err)
		types := p.enabledProfileTypes()
		require.Equal(t, len(allProfileTypes), len(types))
	})

	t.Run("profileTypes", func(t *testing.T) {
		_, err := unstartedProfiler(WithProfileTypes(ProfileType(-1)))
		require.EqualError(t, err, "unknown profile type: -1")
	})
}
