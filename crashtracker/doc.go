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
// # Containers and PID 1
//
// The monitor is a child of the application process, in the same PID
// namespace rather than a separate one. If the application is PID 1 of that
// namespace — the common case for a Go binary run directly as a container's
// ENTRYPOINT with no init process in front of it — the kernel terminates
// every other process in the namespace with an unblockable SIGKILL as soon
// as PID 1 fully exits (pid_namespaces(7)). That includes the monitor.
// Receiving the crash dump itself is not the risk: the runtime's write to
// the pipe happens synchronously before the application finishes exiting,
// with the monitor already started and reading. Uploading the parsed report
// is a network round trip, and it can still be in flight when the namespace
// teardown lands a moment later, silently losing the report. Run the
// application behind an init process (tini, dumb-init, or an orchestrator's
// own, e.g. Kubernetes' shareProcessNamespace) so it is not PID 1 itself —
// already common container practice for signal handling and zombie
// reaping, and required here for the same structural reason.
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
