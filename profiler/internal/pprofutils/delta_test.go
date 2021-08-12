// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

package pprofutils

import (
	"bytes"
	"strings"
	"testing"

	"github.com/matryer/is"
)

func TestDelta(t *testing.T) {
	t.Run("simple", func(t *testing.T) {
		var (
			is        = is.New(t)
			deltaText bytes.Buffer
		)

		profA, err := Text{}.Convert(strings.NewReader(strings.TrimSpace(`
main;foo 5
main;foo;bar 3
main;foobar 4
`)))
		is.NoErr(err)

		profB, err := Text{}.Convert(strings.NewReader(strings.TrimSpace(`
main;foo 8
main;foo;bar 3
main;foobar 5
`)))
		is.NoErr(err)

		delta, err := Delta{}.Convert(profA, profB)
		is.NoErr(err)

		is.NoErr(Protobuf{}.Convert(delta, &deltaText))
		is.Equal(deltaText.String(), strings.TrimSpace(`
main;foo 3
main;foobar 1
`)+"\n")
	})

	t.Run("sample types", func(t *testing.T) {
		var is = is.New(t)

		profA, err := Text{}.Convert(strings.NewReader(strings.TrimSpace(`
x/count y/count
main;foo 5 10
main;foo;bar 3 6
main;foo;baz 9 0
main;foobar 4 8
`)))
		is.NoErr(err)

		profB, err := Text{}.Convert(strings.NewReader(strings.TrimSpace(`
x/count y/count
main;foo 8 16
main;foo;bar 3 6
main;foo;baz 9 0
main;foobar 5 10
`)))
		is.NoErr(err)

		t.Run("happy path", func(t *testing.T) {
			var (
				is        = is.New(t)
				deltaText bytes.Buffer
			)

			deltaConfig := Delta{SampleTypes: []ValueType{{Type: "x", Unit: "count"}}}
			delta, err := deltaConfig.Convert(profA, profB)
			is.NoErr(err)

			is.NoErr(Protobuf{SampleTypes: true}.Convert(delta, &deltaText))
			is.Equal(deltaText.String(), strings.TrimSpace(`
x/count y/count
main;foo 3 16
main;foobar 1 10
main;foo;bar 0 6
`)+"\n")
		})

		t.Run("unknown sample type", func(t *testing.T) {
			var is = is.New(t)
			deltaConfig := Delta{SampleTypes: []ValueType{{Type: "foo", Unit: "count"}}}
			_, err := deltaConfig.Convert(profA, profB)
			is.Equal("One or more sample type(s) was not found in the profile.", err.Error())
		})
	})
}
