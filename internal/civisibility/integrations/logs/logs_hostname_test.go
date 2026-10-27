// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package logs

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLogsInitializeHostnameHelperProcess initializes logs in a fresh process
// and fails if any goroutine runs hostname discovery afterwards.
func TestLogsInitializeHostnameHelperProcess(t *testing.T) {
	if os.Getenv("DD_TEST_LOGS_HOSTNAME_HELPER") != "1" {
		return
	}
	Initialize("hostname-service")
	defer Stop()
	expected, _ := os.Hostname()
	if host != expected {
		os.Stderr.WriteString("host " + host + " != os.Hostname " + expected + "\n")
		os.Exit(3)
	}
	stacks := make([]byte, 1<<20)
	stacks = stacks[:runtime.Stack(stacks, true)]
	if strings.Contains(string(stacks), "/internal/hostname.") {
		os.Stderr.Write(stacks)
		os.Exit(4)
	}
	os.Exit(0)
}

// Initialize uses the OS hostname, which an empty first hostname.Get fell back
// to, without starting the unused probe goroutine that goleak checks report.
func TestInitializeUsesOSHostnameWithoutProbes(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(binary, "-test.run=^TestLogsInitializeHostnameHelperProcess$")
	cmd.Env = append(os.Environ(), "DD_TEST_LOGS_HOSTNAME_HELPER=1", "DD_CIVISIBILITY_LOGS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=false")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "logs initialization:\n%s", out)
}
