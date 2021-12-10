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

func CPURusage(t testing.TB) time.Duration {
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
