// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package osinfo

import "golang.org/x/sys/unix"

// productVersion returns the macOS product version, for example "15.1".
//
// It reads a sysctl because the package init function calls it in every program
// that links this package. Running sw_vers there adds a process start to each
// program start, even in programs that never use the OS version.
func productVersion() (string, error) {
	return unix.Sysctl("kern.osproductversion")
}
