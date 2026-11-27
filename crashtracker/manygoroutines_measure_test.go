// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"bytes"
	"fmt"
	"runtime"
	"testing"
	"time"
)

func TestParseCrashDumpManyGoroutinesStaysWithinSupervisorMemoryBudget(t *testing.T) {
	const targetSize = 31 * 1024 * 1024 // just under maxCrashDumpSize (32 MiB)
	var buf bytes.Buffer
	buf.WriteString("SIGQUIT: quit\n\n")
	goroutineNum := 1
	for buf.Len() < targetSize {
		fmt.Fprintf(&buf, "goroutine %d [chan receive]:\n", goroutineNum)
		fmt.Fprintf(&buf, "main.worker()\n\t/tmp/crashdemo/main.go:%d +0x1c\n", goroutineNum)
		fmt.Fprintf(&buf, "created by main.main in goroutine 1\n\t/tmp/crashdemo/main.go:5 +0x24\n\n")
		goroutineNum++
	}
	dump := buf.Bytes()
	t.Logf("synthetic dump: %d bytes, %d goroutines", len(dump), goroutineNum-1)

	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	peak := baseline.HeapAlloc
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		last := baseline
		sample := func() {
			var current runtime.MemStats
			runtime.ReadMemStats(&current)
			// Add all allocations since the last sample to bound peaks between samples.
			peak = max(peak, last.HeapAlloc+(current.TotalAlloc-last.TotalAlloc))
			last = current
		}
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				sample()
				return
			case <-ticker.C:
				sample()
			}
		}
	}()

	report := parseCrashDump(dump)
	close(stop)
	<-done

	peakAboveBaseline := peak - baseline.HeapAlloc
	t.Logf("threads parsed (pre-cap would have been %d, capThreads reduced to): %d", goroutineNum-1, len(report.Error.Threads))
	t.Logf("upper bound on HeapAlloc above baseline during parseCrashDump: %d bytes (%.1f MiB)", peakAboveBaseline, float64(peakAboveBaseline)/(1024*1024))

	if peakAboveBaseline > supervisorMemoryLimit {
		t.Errorf("peak parsing allocation bound = %.1f MiB, want under the supervisor's %.0f MiB GOMEMLIMIT budget for this many-shallow-goroutines shape",
			float64(peakAboveBaseline)/(1024*1024), float64(supervisorMemoryLimit)/(1024*1024))
	}
}
