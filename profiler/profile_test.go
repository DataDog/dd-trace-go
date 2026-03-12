// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/profiler/internal/pprofutils"

	pprofile "github.com/google/pprof/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeltaProfile(t *testing.T) {
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
contentions/count delay/nanoseconds
main 3 1
main;bar 2 1
main;foo 5 1
`,
			},
			Prof2: textProfile{
				Time: timeB,
				Text: `
contentions/count delay/nanoseconds
main 4 1
main;bar 2 1
main;foo 8 1
main;foobar 7 1
`,
			},
			WantDelta: textProfile{
				Time: timeA,
				Text: `
contentions/count delay/nanoseconds
main;foobar 7 1
main;foo 3 0
main 1 0
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

	deltaProfiler := func(t *testing.T, prof1, prof2 []byte, pt ProfileType, delta bool) (out1 []byte, out2 []byte) {
		p := [][]byte{prof1, prof2}
		testLookupProfile = func(name string, w io.Writer, debug int) error {
			data := p[0]
			if len(p) > 1 {
				p = p[1:]
			}
			_, err := w.Write(data)
			return err
		}
		t.Cleanup(func() { testLookupProfile = nil })
		attachment := pt.Filename()
		if delta {
			attachment = "delta-" + attachment
		}
		backend := startTestProfiler(t, 2, WithProfileTypes(pt), WithPeriod(10*time.Millisecond), WithDeltaProfiles(delta))
		out1 = backend.ReceiveProfile(t).attachments[attachment]
		out2 = backend.ReceiveProfile(t).attachments[attachment]
		return
	}

	t.Setenv("DD_PROFILING_DEBUG_COMPRESSION_SETTINGS", "legacy")
	for _, test := range tests {
		for _, profType := range test.Types {
			t.Run(profType.String(), func(t *testing.T) {
				t.Run("enabled", func(t *testing.T) {
					prof1 := test.Prof1.Protobuf()
					prof2 := test.Prof2.Protobuf()
					out1, out2 := deltaProfiler(t, prof1, prof2, profType, true)
					// First profile is the same since there is no basis for delta
					requirePprofEqual(t, prof1, out1)

					// Compare the text rather than protobuf for the delta,
					// and we'll separately check the timestamp below
					require.Equal(t, test.WantDelta.String(), protobufToText(out2))

					// check delta prof details like timestamps and duration
					deltaProf, err := pprofile.ParseData(out2)
					require.NoError(t, err)
					require.Equal(t, test.Prof2.Time.UnixNano(), deltaProf.TimeNanos)
					require.Equal(t, deltaPeriod.Nanoseconds(), deltaProf.DurationNanos)
				})

				t.Run("disabled", func(t *testing.T) {
					prof1 := test.Prof1.Protobuf()
					prof2 := test.Prof2.Protobuf()
					out1, out2 := deltaProfiler(t, prof1, prof2, profType, false)
					requirePprofEqual(t, prof1, out1)
					requirePprofEqual(t, prof2, out2)
				})
			})
		}
	}
}

func TestGoroutineWait(t *testing.T) {
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

	testLookupProfile = func(_ string, w io.Writer, _ int) error {
		_, err := w.Write([]byte(sample))
		return err
	}
	t.Cleanup(func() { testLookupProfile = nil })

	t.Setenv("DD_PROFILING_WAIT_PROFILE", "true")
	// Use gzip compression since the Google pprof package doesn't understand zstd
	t.Setenv("DD_PROFILING_DEBUG_COMPRESSION_SETTINGS", "legacy")
	backend := startTestProfiler(t, 1, WithPeriod(10*time.Millisecond))

	// pro tip: enable line below to inspect the pprof output using cli tools
	// os.WriteFile(prof.name, prof.data, 0644)

	requireFunctions := func(t *testing.T, s *pprofile.Sample, want []string) {
		t.Helper()
		var got []string
		for _, loc := range s.Location {
			got = append(got, loc.Line[0].Function.Name)
		}
		require.Equal(t, want, got)
	}

	pp, err := pprofile.Parse(bytes.NewReader(backend.ReceiveProfile(t).attachments["goroutineswait.pprof"]))
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
}

func TestGoroutineWaitLimit(t *testing.T) {
	// spawGoroutines spawns n goroutines, waits for them to start executing,
	// and then returns a func to stop them. For more details about `executing`
	// see:
	// https://github.com/DataDog/dd-trace-go/pull/942#discussion_r656924335
	spawnGoroutines := func(n int) func() {
		executing := make(chan struct{})
		stopping := make(chan struct{})
		for range n {
			go func() {
				executing <- struct{}{}
				stopping <- struct{}{}
			}()
			<-executing
		}
		return func() {
			for range n {
				<-stopping
			}
		}
	}

	goroutines := 100
	limit := 10

	stop := spawnGoroutines(goroutines)
	defer stop()

	rl := &log.RecordLogger{}
	defer log.UseLogger(rl)()

	t.Setenv("DD_PROFILING_WAIT_PROFILE_MAX_GOROUTINES", strconv.Itoa(limit))
	t.Setenv("DD_PROFILING_WAIT_PROFILE", "true")
	backend := startTestProfiler(t, 1, WithPeriod(10*time.Millisecond))
	// Wait for two profiles so we can be sure we would have logged the error from the first one
	assert.NotContains(t, backend.ReceiveProfile(t).attachments, "goroutineswait.pprof")
	assert.NotContains(t, backend.ReceiveProfile(t).attachments, "goroutineswait.pprof")

	log.Flush()
	logs := rl.Logs()
	for _, l := range logs {
		_, after, found := strings.Cut(l, "skipping goroutines wait profile: ")
		if !found {
			continue
		}
		var errRoutines, errLimit int
		msg := "%d goroutines exceeds DD_PROFILING_WAIT_PROFILE_MAX_GOROUTINES limit of %d"
		fmt.Sscanf(after, msg, &errRoutines, &errLimit)
		require.GreaterOrEqual(t, errRoutines, goroutines)
		require.Equal(t, limit, errLimit)
		return
	}
	t.Errorf("did not see expected error log, got %s", logs)
}

func Test_goroutineDebug2ToPprof_CrashSafety(t *testing.T) {
	err := goroutineDebug2ToPprof(panicReader{}, io.Discard, time.Time{})
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

func TestUnmarshalText(t *testing.T) {
	tests := []struct {
		Text            []byte
		WantProfileType ProfileType
	}{
		{
			Text:            []byte("cpu"),
			WantProfileType: CPUProfile,
		},
		{
			Text:            []byte("heap"),
			WantProfileType: HeapProfile,
		},
		{
			Text:            []byte("mutex"),
			WantProfileType: MutexProfile,
		},
		{
			Text:            []byte("goroutine"),
			WantProfileType: GoroutineProfile,
		},
		{
			Text:            []byte("block"),
			WantProfileType: BlockProfile,
		},
	}

	for _, test := range tests {
		var p ProfileType
		err := p.UnmarshalText(test.Text)
		require.NoError(t, err)
		assert.Equal(t, test.WantProfileType, p)
	}
}

func requirePprofEqual(t *testing.T, a, b []byte) {
	t.Helper()
	pprofA, err := pprofile.ParseData(a)
	require.NoError(t, err)
	pprofB, err := pprofile.ParseData(b)
	require.NoError(t, err)
	pprofA.Scale(-1)
	pprofDiff, err := pprofile.Merge([]*pprofile.Profile{pprofA, pprofB})
	require.NoError(t, err)
	require.Len(t, pprofDiff.Sample, 0)
}
