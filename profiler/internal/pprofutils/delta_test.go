// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

package pprofutils

import (
	"bytes"
	"strings"
	"testing"

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
}
