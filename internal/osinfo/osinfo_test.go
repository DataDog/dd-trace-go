// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package osinfo_test

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/mod/semver"

	"github.com/DataDog/dd-trace-go/v2/internal/osinfo"
)

func Test(t *testing.T) {
	var (
		osName        = osinfo.OSName()
		osVersion     = osinfo.OSVersion()
		arch          = osinfo.Architecture()
		kernelName    = osinfo.KernelName()
		kernelRelease = osinfo.KernelRelease()
		kernelVersion = osinfo.KernelVersion()
	)

	t.Logf("OS Name: %s\n", osName)
	t.Logf("OS Version: %s\n", osVersion)
	t.Logf("Architecture: %s\n", arch)
	t.Logf("Kernel Name: %s\n", kernelName)
	t.Logf("Kernel Release: %s\n", kernelRelease)
	t.Logf("Kernel Version: %s\n", kernelVersion)

	switch runtime.GOOS {
	case "linux":
		require.Equal(t, "Linux", kernelName)
		require.Truef(t, semver.IsValid("v"+kernelRelease), "invalid kernel version: %s", kernelRelease)
	case "darwin":
		require.Equal(t, "Darwin", kernelName)
		cmd := exec.Command("/usr/bin/sw_vers", "-productVersion")
		cmd.Env = append(cmd.Environ(), "SYSTEM_VERSION_COMPAT=0")
		out, err := cmd.Output()
		require.NoError(t, err)
		require.Equal(t, strings.TrimSpace(string(out)), osVersion)
	}
}

func TestDarwinVersionCompatibility(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("requires macOS")
	}
	// macOS selects compatibility mode at process startup, before osinfo initializes.
	exe, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(exe, "-test.run=^Test$")
	cmd.Env = append(cmd.Environ(), "SYSTEM_VERSION_COMPAT=1", "PATH=")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
}
