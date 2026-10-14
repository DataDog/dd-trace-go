// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build unix

package datastreams

import (
	"syscall"
	"testing"
	"time"
)

func processCPUTime(b *testing.B) time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		b.Fatal(err)
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

// BenchmarkReaderIdleCPU reports the process CPU time used per second while
// the reader has nothing to read.
func BenchmarkReaderIdleCPU(b *testing.B) {
	const idle = 100 * time.Millisecond
	startBenchReader(b)
	time.Sleep(idle)
	var cpu time.Duration
	for b.Loop() {
		before := processCPUTime(b)
		time.Sleep(idle)
		cpu += processCPUTime(b) - before
	}
	b.ReportMetric(float64(cpu.Microseconds())/(float64(b.N)*idle.Seconds()), "cpu-µs/s")
}
