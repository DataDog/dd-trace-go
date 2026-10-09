// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build unix

package datastreams

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
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

// BenchmarkReaderCPU reports process CPU time while writers push at a steady
// rate. Writers sleep between pushes instead of spinning, so their cost is the
// same across reader implementations and the difference between runs is what
// the reader spends waking up and draining. Each op is a 100ms window plus a
// 20ms tail that lets the reader finish the last payloads.
func BenchmarkReaderCPU(b *testing.B) {
	const (
		writers = 4
		window  = 100 * time.Millisecond
		tail    = 20 * time.Millisecond
	)
	for _, rate := range []int{1_000, 10_000, 100_000, 1_000_000} {
		b.Run(fmt.Sprintf("rate=%dk/s", rate/1000), func(b *testing.B) {
			p := startBenchReader(b)
			interval := time.Duration(float64(time.Second) * writers / float64(rate))
			var (
				cpu, wall time.Duration
				pushed    atomic.Int64
			)
			for b.Loop() {
				beforeCPU, start := processCPUTime(b), time.Now()
				var wg sync.WaitGroup
				for range writers {
					wg.Go(func() {
						// Keep to an absolute schedule; after an oversleep the writer
						// catches up in a short burst.
						for next := start; next.Sub(start) < window; next = next.Add(interval) {
							if d := time.Until(next); d > 0 {
								time.Sleep(d)
							}
							p.SetCheckpointWithParams(context.Background(), options.CheckpointParams{PayloadSize: 1000}, "type:edge-1", "direction:in", "type:kafka", "topic:topic1", "group:group1")
							pushed.Add(1)
						}
					})
				}
				wg.Wait()
				time.Sleep(tail)
				cpu += processCPUTime(b) - beforeCPU
				wall += time.Since(start)
			}
			b.ReportMetric(float64(cpu.Nanoseconds())/float64(pushed.Load()), "cpu-ns/payload")
			b.ReportMetric(100*cpu.Seconds()/wall.Seconds(), "cpu-%")
			b.ReportMetric(100*float64(p.stats.dropped.Load())/float64(pushed.Load()), "drop-%")
		})
	}
}
