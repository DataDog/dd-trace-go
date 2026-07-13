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
//
// # Configuration
//
// The monitor process inherits all environment variables except GOMEMLIMIT and
// GOGC. Programmatic options passed to Start (e.g. WithAPIKey, WithAgentURL)
// are applied in the application process and are NOT forwarded to the monitor
// child because they cannot cross process boundaries. Use the corresponding
// DD_* environment variables (DD_API_KEY, DD_TRACE_AGENT_URL, DD_SITE) to
// configure the monitor's upload destination when env-var-free programmatic
// options are required.
//
// # Goroutine stack completeness
//
// By default Go uses GOTRACEBACK=single, which records only the crashing
// goroutine in the crash dump. Set GOTRACEBACK=all in the process environment
// to include all goroutines in the crash report's error.threads field.
//
// # Init order note
//
// The package init() intercepts the monitor role before most user package
// init()s, but Go's initialization order across packages at the same import
// level is deterministic by import path, not guaranteed to place crashtracker
// first. In practice the monitor is intercepted before user code runs in
// orchestrion-injected binaries; manual callers should ensure Start is the
// first statement of main() to minimise exposure.
package crashtracker
