// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"bytes"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// TestParseCrashDumpManyGoroutinesStaysWithinMonitorMemoryBudget answers a
// resource-validation question raised in review (not a demonstrated bug):
// whether a near-32MiB dump containing many shallow goroutines -- as opposed
// to one deeply recursive goroutine, a different allocation pattern -- pushes
// peak parsing memory past the monitor's 256MiB GOMEMLIMIT, since capThreads
// runs after parseThreads has already allocated every goroutine's frames.
// Measured directly rather than assumed: peak HeapAlloc for a ~213k-goroutine
// dump is ~124MiB, under half the budget, so the threshold below has real
// margin against measurement noise from the sampling goroutine's timing.
func TestParseCrashDumpManyGoroutinesStaysWithinMonitorMemoryBudget(t *testing.T) {
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

	var peakHeapAlloc uint64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var m runtime.MemStats
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				runtime.ReadMemStats(&m)
				for {
					old := atomic.LoadUint64(&peakHeapAlloc)
					if m.HeapAlloc <= old || atomic.CompareAndSwapUint64(&peakHeapAlloc, old, m.HeapAlloc) {
						break
					}
				}
			}
		}
	}()

	var baseline runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&baseline)

	report := parseCrashDump(dump)

	close(stop)
	<-done

	const monitorGOMEMLIMITBytes = 256 * 1024 * 1024
	peakAboveBaseline := peakHeapAlloc - baseline.HeapAlloc
	t.Logf("threads parsed (pre-cap would have been %d, capThreads reduced to): %d", goroutineNum-1, len(report.Error.Threads))
	t.Logf("peak HeapAlloc above baseline during parseCrashDump: %d bytes (%.1f MiB)", peakAboveBaseline, float64(peakAboveBaseline)/(1024*1024))

	if peakAboveBaseline > monitorGOMEMLIMITBytes {
		t.Errorf("peak parsing allocation = %.1f MiB, want under the monitor's %.0f MiB GOMEMLIMIT budget for this many-shallow-goroutines shape",
			float64(peakAboveBaseline)/(1024*1024), float64(monitorGOMEMLIMITBytes)/(1024*1024))
	}
}
