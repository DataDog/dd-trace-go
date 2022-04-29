// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"bytes"
	"io/ioutil"
	"testing"
	"time"

	pprofile "github.com/google/pprof/profile"
	"github.com/stretchr/testify/require"
)

func TestGoroutineDebug2ToPprof(t *testing.T) {
	sample := []byte(`
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
`)

	output := new(bytes.Buffer)

	err := goroutineDebug2ToPprof(bytes.NewReader(sample), output, time.Time{})
	require.NoError(t, err)

	requireFunctions := func(t *testing.T, s *pprofile.Sample, want []string) {
		t.Helper()
		var got []string
		for _, loc := range s.Location {
			got = append(got, loc.Line[0].Function.Name)
		}
		require.Equal(t, want, got)
	}

	pp, err := pprofile.Parse(output)
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
