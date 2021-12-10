// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

package traceproftest

import (
	"strconv"
	"syscall"
	"testing"
	"time"
)

// ValidSpanID returns true if id is a valid span id (random.Uint64()).
func ValidSpanID(id string) bool {
	val, err := strconv.ParseUint(id, 10, 64)
	return err == nil && val > 0
}

// CPURusage returns the amount of On-CPU time for the process (sys+user) since
// it has been started. It uses getrusage(2).
func CPURusage(t testing.TB) time.Duration {
	// Note: If this becomes a portability issue in the future (windows?) it's okay
	// to implement a non-working version for those platforms. We only use this for
	// benchmarking on linux right now.
	var rusage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &rusage); err != nil {
		t.Fatal(err)
		panic(err)
	}
	return timevalDuration(rusage.Stime) + timevalDuration(rusage.Utime)
}

func timevalDuration(tv syscall.Timeval) time.Duration {
	return time.Duration(tv.Nano())
}
