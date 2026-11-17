// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// Package crashtracker sends structured crash reports to Datadog Error Tracking.
//
// Start re-executes the application as a child with the same arguments,
// environment, and standard streams. The original process becomes a supervisor.
// It reads the child's crash dump through a pipe registered with
// [runtime/debug.SetCrashOutput]. It parses and uploads the report before it
// exits. The upload phase has an overall deadline of 10 seconds, including
// retries. A normal exit does not produce a report.
//
// # Lifecycle
//
// Start must be the first statement in main:
//
//	func main() {
//	    if err := crashtracker.Start(); err != nil {
//	        log.Printf("crashtracker.Start: %v", err)
//	    }
//
//	    // Application code starts here.
//	}
//
// All package initialization and any code before Start run twice, once in the
// supervisor and once in the application. Keep externally visible work after
// Start. Crashes during package initialization or before Start are not captured.
// The supervisor does not return from Start. The child registers the inherited
// crash pipe, removes the internal marker, and returns. Application subprocesses
// do not inherit the crash pipe or the marker. If startup fails, Start returns an
// error so the application can continue without crash tracking.
//
// There is no Stop. Process exit closes the pipe. Do not defer an unregister
// call. Deferred functions run before the runtime writes a panic dump.
// Start is idempotent. Later calls return the result of the first call.
//
// # Process lifecycle and containers
//
// The application runs as a child, so its PID differs from the container main
// PID. In the application, os.Getppid() returns the supervisor's PID. The
// supervisor stays alive until the upload finishes or its deadline expires.
// This keeps container teardown from interrupting the upload after a crash.
//
// On Unix, the child has its own process group. If stdin is a terminal and the
// supervisor is in its foreground group, the child's group takes the foreground.
// The supervisor forwards external signals to the child's PID.
// It excludes runtime signals, job-control input
// and output signals, and synchronous fault signals. On Windows, console Ctrl
// events reach both processes, so the supervisor ignores os.Interrupt and does
// not forward signals. The supervisor propagates the child's exit code. On Unix,
// it re-raises SIGHUP, SIGINT, SIGTERM, or SIGKILL if that signal killed the child.
// For other signals, it exits with code 128 plus the signal number. It does not
// re-raise signals that could produce a second crash dump from the supervisor.
// On Linux, the kernel kills the child if the supervisor dies.
//
// # Configuration
//
// The supervisor resolves environment settings and Start options before it
// starts the child. It uses that configuration for the upload in the same
// process, including a custom client from WithHTTPClient. The child ignores
// Start options. DD_CRASHTRACKING_ENABLED=false disables supervision unless
// WithEnabled overrides it.
//
// # Orchestrion
//
// The Orchestrion integration injects Start as the first statement of main.
// It uses DD_* environment configuration. It is opt-in. The
// github.com/DataDog/dd-trace-go/orchestrion/all package does not include it.
// To enable it, import this package in orchestrion.tool.go:
//
//	import _ "github.com/DataDog/dd-trace-go/v2/crashtracker" // integration
//
// A later Start call with options is a no-op. Use environment configuration
// instead of programmatic options with this integration.
//
// # Goroutine stack completeness
//
// Go uses GOTRACEBACK=single by default. Set GOTRACEBACK=all in the process
// environment to include all goroutines in the report's error.threads field.
package crashtracker
