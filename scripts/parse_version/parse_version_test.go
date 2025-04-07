// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

//go:build scripts
// +build scripts

package main

import (
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// This tests that the parse_version.go output respects the expected format for github actions
func TestOutput(t *testing.T) {
	t.Run("unit", func(t *testing.T) {
		require.Equal(t, ghOutput("test", "test"), "::set-output name=test::test\n")
	})

	t.Run("main", func(t *testing.T) {
		// Capture stdout before running main
		stdout := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w
		main()
		w.Close()
		os.Stdout = stdout
		bytes, _ := io.ReadAll(r)
		r.Close()

		for _, str := range []string{
			"current",
			"current_without_rc_suffix",
			"current_without_patch",
			"next_minor",
			"next_patch",
			"next_rc",
		} {
			line := fmt.Sprintf("::set-output name=%s::", str)
			require.Contains(t, string(bytes), line)
		}
	})
}
