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

func TestTextConvert(t *testing.T) {
	t.Run("simple", func(t *testing.T) {
		is := is.New(t)
		textIn := strings.TrimSpace(`
main;foo 5
main;foobar 4
main;foo;bar 3
`)
		proto, err := Text{}.Convert(strings.NewReader(textIn))
		is.NoErr(err)
		textOut := bytes.Buffer{}
		is.NoErr(Protobuf{}.Convert(proto, &textOut))
		is.Equal(textIn+"\n", textOut.String())
	})

	t.Run("header with one sample type", func(t *testing.T) {
		is := is.New(t)
		textIn := strings.TrimSpace(`
samples/count
main;foo 5
main;foobar 4
main;foo;bar 3
	`)
		proto, err := Text{}.Convert(strings.NewReader(textIn))
		is.NoErr(err)
		textOut := bytes.Buffer{}
		is.NoErr(Protobuf{SampleTypes: true}.Convert(proto, &textOut))
		is.Equal(textIn+"\n", textOut.String())
	})

	t.Run("header with multiple sample types", func(t *testing.T) {
		is := is.New(t)
		textIn := strings.TrimSpace(`
samples/count duration/nanoseconds
main;foo 5 50000000
main;foobar 4 40000000
main;foo;bar 3 30000000
	`)
		proto, err := Text{}.Convert(strings.NewReader(textIn))
		is.NoErr(err)
		textOut := bytes.Buffer{}
		is.NoErr(Protobuf{SampleTypes: true}.Convert(proto, &textOut))
		is.Equal(textIn+"\n", textOut.String())
	})
}
