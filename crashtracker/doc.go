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
// configuration; where that integration is built in, it wins the race to be
// the first Start call, so a later programmatic Start call with options in
// main is a no-op and those options are silently dropped — not applied, not
// merged, and not reported as ignored. Do not rely on programmatic options to
// control startup in that build; use the DD_* environment configuration the
// integration reads instead.
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
// # Cgo and foreign threads
//
// A fault while Go is calling into C through cgo is captured normally: the
// Go runtime cannot safely convert it to a recoverable panic, so it prints a
// fatal crash report instead, and that report reaches SetCrashOutput and
// this package's parser the same way an ordinary Go-code fault does.
//
// A fault on a thread created entirely by native code — a pthread spawned
// directly by a C library, which never entered Go — is different.
// SetCrashOutput cannot observe it at all: with no saved native signal
// handler and no signal notification requested, the Go runtime restores the
// default signal action and the process terminates with no Go crash report
// of any kind, silently. [WithForeignThreadSignals] opts into best-effort
// visibility for exactly this case, with real limitations documented on
// that option — it is not a full report and not crash recovery.
//
// # Init order note
//
// The monitor child is intercepted from package init, which is the earliest hook
// available to a pure Go implementation. Go leaves cross-package init order
// unspecified beyond dependency constraints, and in practice this means any
// package linked into the binary — not just main's direct imports, but every
// transitive dependency — can run its init to full completion in the monitor
// role before crashtracker's own init detects that role and exits. This is not
// something reordering your own imports can influence: the ordering is decided
// by the toolchain across the whole import graph, not by import statement order
// in your source. Keep expensive or externally visible init work out of any
// package that might be imported when crashtracking is enabled, and call Start
// as the first statement of main for manual integrations.
package crashtracker
