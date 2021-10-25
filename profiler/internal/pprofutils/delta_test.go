// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

package pprofutils

import (
	"bytes"
	"io/ioutil"
	"os"
	"strings"
	"testing"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/require"
)

func TestDelta(t *testing.T) {
	t.Run("simple", func(t *testing.T) {
		var deltaText bytes.Buffer

		profA, err := Text{}.Convert(strings.NewReader(strings.TrimSpace(`
main;foo 5
main;foo;bar 3
main;foobar 4
`)))
		require.NoError(t, err)

		profB, err := Text{}.Convert(strings.NewReader(strings.TrimSpace(`
main;foo 8
main;foo;bar 3
main;foobar 5
`)))
		require.NoError(t, err)

		delta, err := Delta{}.Convert(profA, profB)
		require.NoError(t, err)

		require.NoError(t, Protobuf{}.Convert(delta, &deltaText))
		require.Equal(t, deltaText.String(), strings.TrimSpace(`
main;foo 3
main;foobar 1
`)+"\n")
	})

	t.Run("sampleTypes", func(t *testing.T) {
		profA, err := Text{}.Convert(strings.NewReader(strings.TrimSpace(`
x/count y/count
main;foo 5 10
main;foo;bar 3 6
main;foo;baz 9 0
main;foobar 4 8
`)))
		require.NoError(t, err)

		profB, err := Text{}.Convert(strings.NewReader(strings.TrimSpace(`
x/count y/count
main;foo 8 16
main;foo;bar 3 6
main;foo;baz 9 0
main;foobar 5 10
`)))
		require.NoError(t, err)

		t.Run("happyPath", func(t *testing.T) {
			var deltaText bytes.Buffer

			deltaConfig := Delta{SampleTypes: []ValueType{{Type: "x", Unit: "count"}}}
			delta, err := deltaConfig.Convert(profA, profB)
			require.NoError(t, err)

			require.NoError(t, Protobuf{SampleTypes: true}.Convert(delta, &deltaText))
			require.Equal(t, deltaText.String(), strings.TrimSpace(`
x/count y/count
main;foo 3 16
main;foobar 1 10
main;foo;bar 0 6
`)+"\n")
		})

		t.Run("unknownSampleType", func(t *testing.T) {
			deltaConfig := Delta{SampleTypes: []ValueType{{Type: "foo", Unit: "count"}}}
			_, err := deltaConfig.Convert(profA, profB)
			require.Equal(t, "one or more sample type(s) was not found in the profile", err.Error())
		})
	})

	// negativeValues tests that delta profile don't produce negative values when
	// filename symbolization is flaky. See PROF-4239 for more details.
	t.Run("negativeValues", func(t *testing.T) {
		mapping := &profile.Mapping{ID: 1}
		fn := &profile.Function{
			ID:       1,
			Name:     "main.main",
			Filename: "main.go",
		}
		location := &profile.Location{
			ID:      1,
			Mapping: mapping,
			Address: 123,
			Line: []profile.Line{{
				Function: fn,
				Line:     23,
			}},
		}
		profA := &profile.Profile{
			SampleType: []*profile.ValueType{{
				Type: "alloc",
				Unit: "objects",
			}},
			Sample: []*profile.Sample{{
				Location: []*profile.Location{location},
				Value:    []int64{5},
			}},
			Mapping:    []*profile.Mapping{mapping},
			Location:   []*profile.Location{location},
			Function:   []*profile.Function{fn},
			PeriodType: &profile.ValueType{},
		}
		profB := profA.Copy()
		profB.Sample[0].Value[0] = 8
		// flaky symbolization: sample with same Address as in profA resolves to
		// a different filename.
		profB.Sample[0].Location[0].Line[0].Function.Filename = "bar.go"

		delta, err := Delta{}.Convert(profA, profB)
		require.NoError(t, err)

		require.Equal(t, len(delta.Sample), 1)
		require.Equal(t, delta.Sample[0].Value[0], int64(3))
	})

	t.Run("negativeValuesFromEnv", func(t *testing.T) {
		inPath := strings.TrimSpace(os.Getenv("DD_TEST_PROF_IN"))
		if inPath == "" {
			t.Skip("DD_TEST_PROF_IN not set")
		}

		inData, err := ioutil.ReadFile(inPath)
		require.NoError(t, err)
		prof, err := profile.ParseData(inData)
		require.NoError(t, err)

		fixNegativeValues(prof)

		for _, s := range prof.Sample {
			if hasNegativeValue(s) {
				t.Fatalf("fixNegativeValues failed to fix sample: %#v", s)
			}
		}

		outPath := strings.TrimSpace(os.Getenv("DD_TEST_PROF_OUT"))
		if outPath != "" {
			var outBuf bytes.Buffer
			require.NoError(t, prof.Write(&outBuf))
			require.NoError(t, ioutil.WriteFile(outPath, outBuf.Bytes(), 0666))
		}
	})
}
