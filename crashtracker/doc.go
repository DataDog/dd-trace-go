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
// Requires runtime/debug.SetCrashOutput, added in Go 1.23; see this repository's
// go.mod for the minimum Go version this module actually builds with.
//
// # Lifecycle
//
// Call Start as early as possible in main, before any goroutines are created:
//
//	func main() {
//	    if err := crashtracker.Start(); err != nil {
//	        log.Printf("crashtracker.Start: %v", err)
//	    }
//
//	    // ... application code
//	}
//
// There is no corresponding Stop. Process exit alone closes the crash pipe,
// which is all the cleanup the monitor needs: it reads EOF and exits without
// filing a report. Do not add a deferred unregister step — deferred functions
// run during panic unwinding, before the runtime writes the crash dump, so a
// defer here would disable reporting for the most common crash: an
// unrecovered panic.
//
// Start is idempotent: subsequent calls after the first are no-ops. A
// companion Orchestrion integration (not part of this package) can inject a
// Start call as the first statement of main using DD_* environment
// configuration; where that integration is built in, a later programmatic
// Start call with options in main is a no-op, so programmatic options should
// not be relied on to control startup in that build.
//
// # Configuration
//
// The monitor process inherits all environment variables except GOMEMLIMIT and
// GOGC (the monitor sets its own memory ceiling instead of inheriting the
// application's). Options passed to Start (WithService, WithEnv, WithVersion,
// WithAPIKey, WithAgentURL, WithSite) are resolved in the application process
// and then forwarded to the monitor child across the process boundary, so they
// take effect end to end. WithHTTPClient is the one exception: an *http.Client
// cannot cross a process boundary, so it only affects direct calls to the
// package's internal upload path and has no effect via Start.
//
// # Goroutine stack completeness
//
// By default Go uses GOTRACEBACK=single, which records only the crashing
// goroutine in the crash dump. Set GOTRACEBACK=all in the process environment
// to include all goroutines in the crash report's error.threads field.
//
// # Init order note
//
// The monitor child is intercepted from package init, which is the earliest hook
// available to a pure Go implementation, but Go does not guarantee crashtracker's
// init runs before every other imported package init. Some init side effects in
// packages imported by main can still execute in the monitor child before the
// monitor role exits. Keep expensive or externally visible init work out of
// packages imported by main when crashtracking is enabled, and call Start as the
// first statement of main for manual integrations.
package crashtracker
