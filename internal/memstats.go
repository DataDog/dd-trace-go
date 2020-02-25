// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package internal

import (
	"runtime"
	"sync"
	"time"
)

var (
	mu       sync.Mutex // guards below fields
	lastRead time.Time  // time of most recent read
	memStats = new(runtime.MemStats)
)

// interval specifies the allowed interval at which reading *runtime.MemStats is
// allowed to reduce "stopTheWorld" calls.
const interval = 10 * time.Second

// MemStats returns the most recently read *runtime.MemStats. It only returns
// more recent values if at least 10 seconds have passed since the last read.
func MemStats() *runtime.MemStats {
	mu.Lock()
	defer mu.Unlock()
	now := time.Now()
	if memStats == nil || now.Sub(lastRead) >= interval {
		runtime.ReadMemStats(memStats)
		lastRead = now
	}
	return memStats
}
