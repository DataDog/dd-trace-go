// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// +build !windows,!linux,!darwin,!freebsd

package internal

import (
	"runtime"
)

// OSName detects name of the operating system.
func OSName() string {
	return runtime.GOOS
}

// OSVersion detects version of the operating system.
func OSVersion() string {
	return unknown
}
