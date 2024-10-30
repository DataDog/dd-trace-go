// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package profiler

import (
	"fmt"
	"runtime/debug"
)

// pgoTag returns a tag indicating whether the program was built with
// profile-guided optimization.
func pgoTag() string {
	return fmt.Sprintf("pgo:%t", pgoEnabled())
}

// pgoEnabled returns true if the program was built with profile-guided
// optimization.
func pgoEnabled() bool {
	info, ok := debug.ReadBuildInfo()
	if ok {
		for _, bs := range info.Settings {
			if bs.Key == "-pgo" {
				return bs.Value != ""
			}
		}
	}
	return false
}
