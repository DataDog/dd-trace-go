// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package ossec

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExecCommandVector_forcesExecutableNameAsArgv0(t *testing.T) {
	tests := []struct {
		name string
		exe  string
		argv []string
		want []string
	}{
		{
			name: "replaces spoofed argv0 with executed binary",
			exe:  "/usr/bin/wget",
			argv: []string{"ls", "-la"},
			want: []string{"/usr/bin/wget", "-la"},
		},
		{
			name: "keeps normal argv unchanged",
			exe:  "/usr/bin/touch",
			argv: []string{"/usr/bin/touch", "/tmp/passwd"},
			want: []string{"/usr/bin/touch", "/tmp/passwd"},
		},
		{
			name: "uses executable name when argv is empty",
			exe:  "/bin/sh",
			argv: nil,
			want: []string{"/bin/sh"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			argv := append([]string(nil), tt.argv...)

			// When
			got := execCommandVector(tt.exe, argv)

			// Then
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.argv, argv)
		})
	}
}
