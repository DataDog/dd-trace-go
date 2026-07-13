// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// Package crashtracker monitors the application process for crashes and sends
// structured crash reports to Datadog Error Tracking.
//
// It uses the monitor-process pattern: on Start, the application re-execs itself
// as a lightweight monitor child (identified by the DD_CRASHTRACKING_IS_MONITOR_PROCESS
// environment variable). The monitor child inherits a pipe fd registered via
// [runtime/debug.SetCrashOutput]; when the application crashes the Go runtime writes
// the crash dump to that pipe and the monitor child parses and uploads a structured
// report to the Error Tracking intake.
//
// Requires Go 1.23 or later (SetCrashOutput was added in Go 1.23).
//
// # Lifecycle
//
// Call Start as early as possible in main, before any goroutines are created, and
// defer Stop to ensure the monitor is released on clean exit:
//
//	func main() {
//	    if err := crashtracker.Start(); err != nil {
//	        log.Printf("crashtracker.Start: %v", err)
//	    }
//	    defer crashtracker.Stop()
//
//	    // ... application code
//	}
//
// Start is idempotent: subsequent calls after the first are no-ops.
package crashtracker
