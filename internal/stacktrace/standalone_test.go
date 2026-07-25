// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package stacktrace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStandaloneImportsHonorStackTraceEnvironment(t *testing.T) {
	const configPackage = "github.com/DataDog/dd-trace-go/v2/internal/config"
	deps := exec.Command("go", "list", "-deps", "./testdata/standalone")
	output, err := deps.CombinedOutput()
	require.NoError(t, err, string(output))
	require.NotContains(t, strings.Split(string(output), "\n"), configPackage)

	binary := filepath.Join(t.TempDir(), "standalone")
	build := exec.Command("go", "build", "-o", binary, "./testdata/standalone")
	output, err = build.CombinedOutput()
	require.NoError(t, err, string(output))

	for _, tc := range []struct {
		name    string
		env     []string
		want    string
		wantLog string
	}{
		{
			name: "disabled",
			env:  []string{"DD_APPSEC_STACK_TRACE_ENABLED=false"},
			want: "enabled=false\n",
		},
		{
			name: "depth",
			env: []string{
				"DD_APPSEC_STACK_TRACE_ENABLED=true",
				"DD_APPSEC_MAX_STACK_TRACE_DEPTH=1",
			},
			want: "enabled=true depth=1\n",
		},
		{
			name: "invalid-enabled-logs-once",
			env:  []string{"DD_APPSEC_STACK_TRACE_ENABLED=invalid"},
			want: "enabled=true\n",
			wantLog: "Failed to parse DD_APPSEC_STACK_TRACE_ENABLED env var as boolean:" +
				" (using default value: true)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(binary, tc.name)
			cmd.Env = append(os.Environ(), tc.env...)
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, string(output))
			require.Contains(t, string(output), tc.want)
			if tc.wantLog != "" {
				require.Equal(t, 1, strings.Count(string(output), tc.wantLog), string(output))
			}
		})
	}
}
