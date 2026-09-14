// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package status exposes whether AppSec has ever been enabled. This allows the
// profiler to conservatively determine whether CPU profiles may contain AppSec
// goroutine pprof labels without depending on the full AppSec implementation.
// The monotonic signal intentionally favors simplicity over coordinating an
// exact status across AppSec lifecycle transitions.
package status

import "sync/atomic"

var everEnabled atomic.Bool

// EverEnabled reports whether AppSec has been enabled at least once during the
// lifetime of the process. Once true, it remains true even if AppSec stops or
// is remotely deactivated because goroutine pprof labels added while it was
// enabled may still be present.
func EverEnabled() bool {
	return everEnabled.Load()
}

// MarkEnabled records that AppSec has been enabled.
func MarkEnabled() {
	everEnabled.Store(true)
}
