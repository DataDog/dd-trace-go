// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build unix

package datastreams

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/datastreams/options"
)

func processCPUTime(b *testing.B) time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		b.Fatal(err)
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

// BenchmarkReaderIdleCPU reports the process CPU time used per second while
// the reader has nothing to read. Each op resets the reader's backoff with one
// payload, then measures the backoff up to maxPollInterval plus three polls
// at it.
func BenchmarkReaderIdleCPU(b *testing.B) {
	idle := 3 * maxPollInterval
	for d := minPollInterval; d < maxPollInterval; d *= 2 {
		idle += d
	}
	p := startBenchReader(b)
	var cpu time.Duration
	for b.Loop() {
		want := p.stats.payloadsIn.Load() + 1
		p.SetCheckpointWithParams(context.Background(), options.CheckpointParams{PayloadSize: 1000}, "type:edge-1", "direction:in", "type:kafka", "topic:topic1", "group:group1")
		for p.stats.payloadsIn.Load() < want {
		}
		before := processCPUTime(b)
		time.Sleep(idle)
		cpu += processCPUTime(b) - before
	}
	b.ReportMetric(float64(cpu.Microseconds())/(float64(b.N)*idle.Seconds()), "cpu-µs/s")
}
