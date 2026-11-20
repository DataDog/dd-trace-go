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
// exits. With the default client, the upload phase has an overall deadline of
// 10 seconds, including retries, after a pipe-EOF wait of at most 2 seconds, so
// the exit takes about 12 seconds at most after a crash. The default client
// limits each attempt to 3 seconds. A custom client from WithHTTPClient keeps
// its own per-attempt timeout, and it must honor request contexts for this
// bound. During that time the supervisor ignores stop signals. A normal exit
// does not produce a report.
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
// crash pipe, removes the internal marker, and returns. Subprocesses spawned
// after Start do not inherit the crash pipe or the marker. Code that runs
// before Start, such as package init, can inherit both. The marker names the
// supervisor's PID, and the child takes the child role only if that PID is its
// parent PID. If startup fails, Start returns an error so the application can
// continue without crash tracking.
//
// With a manual Start as the first statement of main, a tracer or profiler that
// main starts later runs only in the application. A tracer or profiler that a
// package init function starts still runs in both processes.
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
// On Unix, terminal mode requires a controlling terminal, a supervisor PID other
// than 1, and a parent PID above 1. The child shares the supervisor's process group
// in terminal mode. Otherwise, detached mode gives the child its own process group.
// Detached mode gives the child the terminal foreground only if the supervisor
// owns it. If the supervisor does not own the terminal foreground, the child
// does not get it either, the same as the application without crash tracking,
// so a terminal read can stop it with SIGTTIN.
//
// Detached mode forwards external signals to the child's PID. Both modes exclude
// runtime signals (SIGCHLD, SIGURG, SIGPROF), SIGPIPE, SIGABRT, SIGTTIN,
// SIGTTOU, and synchronous fault signals.
// Terminal mode also excludes SIGINT, SIGQUIT, SIGHUP, SIGWINCH, SIGTSTP, and SIGCONT.
// It excludes SIGINFO on Darwin and BSDs. The terminal and shell deliver these
// terminal signals to the job group. If you send one only to the supervisor PID,
// the application does not receive it. Other forwarded signals sent to the whole
// job group, such as with kill %1, can reach the application twice.
//
// On Windows, console Ctrl events reach both processes. The supervisor catches
// os.Interrupt with a private handler and does not forward signals.
//
// On Unix, non-close-on-exec inherited descriptors keep their numbers. This
// requires /dev/fd. Without it, Start returns an error and the application
// continues without supervision. After the child starts, the supervisor closes
// its copies of those duplicated descriptors.
//
// For systemd socket activation, the child updates LISTEN_PID to its own PID if
// the value names the supervisor. It does the same for WATCHDOG_PID. It
// preserves LISTEN_FDS and LISTEN_FDNAMES.
//
// Under systemd, the supervisor is the main process of the service, and the
// application sends its notifications from another PID. A service with
// Type=notify or WatchdogSec must set NotifyAccess=all. Otherwise systemd
// rejects READY=1 and the watchdog keep-alives of the application. With
// KillMode=control-group, the default, systemd sends the stop signal to both
// processes. The supervisor also forwards it, so the application can receive
// SIGTERM twice.
//
// The supervisor propagates the child's exit code. On Unix,
// it re-raises SIGHUP, SIGINT, SIGTERM, or SIGKILL if that signal killed the child.
// For other signals, it exits with code 128 plus the signal number. It does not
// re-raise signals that could produce a second crash dump from the supervisor.
// On Linux, the kernel kills the child if the supervisor dies. On other
// platforms the child keeps running without crash reporting. On those platforms,
// if the supervisor dies before the child calls Start, the child sees a stray
// marker and supervises itself, so the application runs one generation deeper.
//
// # Cost
//
// Supervision adds one process and one fork/exec to every start. Short-lived
// command-line tools and serverless functions pay most, because the extra init
// and fork/exec are a large part of their run time. Init-heavy binaries also pay
// more, because the supervisor keeps its init heap for the lifetime of the
// application.
//
// The supervisor's memory limit is soft. Near the 256 MiB limit the runtime
// throttles through GC assist. The real peak adds the supervisor's init heap
// and the raw dump to the parse cost that the measure test bounds.
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
// With this integration, the supervisor does not start the tracer or the
// profiler. The application starts both. If supervision fails, Start returns
// an error, and the tracer and profiler start at the Start call site instead
// of in init.
//
// # Goroutine stack completeness
//
// Go uses GOTRACEBACK=single by default. Set GOTRACEBACK=all in the process
// environment to include all goroutines in the report's error.threads field.
package crashtracker
