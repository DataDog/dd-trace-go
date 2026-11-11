// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package utils

import (
	"path/filepath"
	"strings"
)

// differentFilesystems reports whether a and b are known to be on different
// volumes, where renaming from one to the other fails.
func differentFilesystems(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return false
	}
	volumeA, volumeB := filepath.VolumeName(absA), filepath.VolumeName(absB)
	return volumeA != "" && volumeB != "" && !strings.EqualFold(volumeA, volumeB)
}
