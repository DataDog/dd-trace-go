// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	// monitorEnvVar is the environment variable set on the monitor child process.
	monitorEnvVar = "DD_CRASHTRACKING_IS_MONITOR_PROCESS"

	// The following internal-only env vars forward the application process's
	// resolved config (env defaults plus any Start options) to the monitor
	// child, which is a separate process and cannot see Go-level Option values
	// directly. Like monitorEnvVar, they are read via raw os.Getenv rather than
	// the env validation layer, are never documented as user-facing
	// configuration, and exist purely to cross the process boundary.
	monitorServiceEnvVar  = "DD_CRASHTRACKING_MONITOR_SERVICE"
	monitorEnvEnvVar      = "DD_CRASHTRACKING_MONITOR_ENV"
	monitorVersionEnvVar  = "DD_CRASHTRACKING_MONITOR_VERSION"
	monitorAPIKeyEnvVar   = "DD_CRASHTRACKING_MONITOR_API_KEY"
	monitorSiteEnvVar     = "DD_CRASHTRACKING_MONITOR_SITE"
	monitorAgentURLEnvVar = "DD_CRASHTRACKING_MONITOR_AGENT_URL"

	// maxCrashDumpSize is the maximum number of bytes read from the crash pipe. It
	// bounds memory usage in the monitor for very large crash dumps.
	maxCrashDumpSize = 32 * 1024 * 1024 // 32 MiB

	// monitorGOMEMLIMIT is an explicit soft memory ceiling for the monitor child.
	// The monitor drops the app's inherited GOMEMLIMIT (see buildChildEnv) but
	// still needs its own bound: it holds the crash dump, the parsed frames, and
	// the marshalled report body concurrently, and an unbounded monitor can
	// itself contribute to the OOM condition it exists to report on.
	monitorGOMEMLIMIT = "256MiB"
)

// isMonitorProcess reports whether the current process is the monitor child.
//
// It reads the marker with os.Getenv directly rather than through the env
// validation layer because this check runs before the tracer's configuration
// machinery is initialised.
func isMonitorProcess() bool {
	return os.Getenv(monitorEnvVar) == "1" //nolint:forbidigo
}

// monitorConfigFromEnv reconstructs the config forwarded by buildChildEnv. It
// is the monitor-side counterpart to Start's opts: since the app process
// already resolved env defaults and applied any WithX options into a single
// config before spawning, the monitor only needs to read back that resolution
// rather than repeat it. WithHTTPClient cannot cross the process boundary and
// is never forwarded; the monitor always builds its own HTTP client.
func monitorConfigFromEnv() *config {
	return &config{
		service:  os.Getenv(monitorServiceEnvVar),  //nolint:forbidigo
		env:      os.Getenv(monitorEnvEnvVar),      //nolint:forbidigo
		version:  os.Getenv(monitorVersionEnvVar),  //nolint:forbidigo
		apiKey:   os.Getenv(monitorAPIKeyEnvVar),   //nolint:forbidigo
		site:     os.Getenv(monitorSiteEnvVar),     //nolint:forbidigo
		agentURL: os.Getenv(monitorAgentURLEnvVar), //nolint:forbidigo
	}
}

// runMonitor is the monitor-child entry point. It reads crash output from stdin,
// parses it, and uploads a report. It never returns.
func runMonitor(cfg *config) {
	// A signal delivered to the whole process group (a supervisor forwarding
	// Ctrl-\, or dumb-init without --single-child) would otherwise kill this
	// process at the same moment the app dumps, before it can upload. The
	// monitor is also detached into its own process group in spawnMonitor;
	// ignoring these here covers the window between fork and that detach, and
	// any signal that still reaches the group despite it.
	ignoreTerminalSignals()

	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxCrashDumpSize))
	// Drain any remaining crash-dump bytes after the cap so the crashing
	// application is not blocked writing into the pipe while we upload.
	go io.Copy(io.Discard, os.Stdin) //nolint:errcheck
	if err != nil || len(data) == 0 {
		// A read error or an empty buffer means the application exited cleanly
		// without writing a crash dump; there is nothing to report.
		os.Exit(0)
	}

	report := parseCrashDump(data)
	report.DDTags = buildDDTags(cfg, report)
	if err := uploadReport(cfg, report); err != nil {
		// Emit one line so operators know a crash report was attempted but
		// failed — without this, the failure is invisible. Routed through the
		// shared logger (see spawnMonitor) rather than a raw os.Stderr write.
		log.Warn("crashtracker: upload failed: %v", err.Error())
	}
	os.Exit(0)
}

// buildChildEnv builds the environment for the monitor child process. It copies
// the parent env, drops runtime-tuning variables that would misconfigure the
// lightweight monitor (see golang/go#73490), sets an explicit memory ceiling,
// sets the monitor marker, and forwards cfg's resolved fields so the monitor's
// upload destination and tags match what the application process resolved.
func buildChildEnv(cfg *config) []string {
	parentEnv := os.Environ()
	childEnv := make([]string, 0, len(parentEnv)+8)
	for _, kv := range parentEnv {
		// GOMEMLIMIT and GOGC are tuned for the application's workload; applying
		// them to the monitor can starve it or trigger GC pathologies.
		if strings.HasPrefix(kv, "GOMEMLIMIT=") || strings.HasPrefix(kv, "GOGC=") {
			continue
		}
		childEnv = append(childEnv, kv)
	}
	childEnv = append(childEnv,
		"GOMEMLIMIT="+monitorGOMEMLIMIT,
		monitorEnvVar+"=1",
	)
	forward := func(key, value string) {
		if value != "" {
			childEnv = append(childEnv, key+"="+value)
		}
	}
	forward(monitorServiceEnvVar, cfg.service)
	forward(monitorEnvEnvVar, cfg.env)
	forward(monitorVersionEnvVar, cfg.version)
	forward(monitorAPIKeyEnvVar, cfg.apiKey)
	forward(monitorSiteEnvVar, cfg.site)
	forward(monitorAgentURLEnvVar, cfg.agentURL)
	return childEnv
}

// spawnMonitor re-execs the current binary as a monitor child, sets up a pipe,
// and registers the pipe write end with runtime/debug.SetCrashOutput.
func spawnMonitor(cfg *config) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("crashtracker: get executable path: %w", err)
	}

	cmd := exec.Command(exe) //nolint:gosec // re-execing our own binary is intentional
	cmd.Env = buildChildEnv(cfg)

	// Detach the monitor into its own process group so terminal signals and
	// group-directed signals (Ctrl-\, a supervisor's SIGTERM broadcast) do not
	// reach it independently of the app; ignoreTerminalSignals (in runMonitor)
	// covers the remaining window before this takes effect.
	detachProcessGroup(cmd)

	// Route the monitor's own stdout/stderr through a line-buffered forwarder
	// instead of the app's raw os.Stderr fd: two processes writing the same fd
	// with no synchronisation can interleave at byte granularity and corrupt
	// whatever either of them was writing, including the app's own crash dump.
	diagStdout, diagStderr, err := newDiagnosticsForwarder()
	if err != nil {
		return fmt.Errorf("crashtracker: create diagnostics pipe: %w", err)
	}
	cmd.Stdout = diagStdout
	cmd.Stderr = diagStderr

	// StdinPipe wires the child's stdin; do not set cmd.Stdin separately.
	pipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("crashtracker: create stdin pipe: %w", err)
	}

	// exec.Cmd.StdinPipe returns an *os.File backed by os.Pipe. SetCrashOutput
	// requires an *os.File, so assert the concrete type rather than panicking.
	pipeFile, ok := pipe.(*os.File)
	if !ok {
		_ = pipe.Close()
		return fmt.Errorf("crashtracker: stdin pipe is not *os.File (type %T)", pipe)
	}

	if err := debug.SetCrashOutput(pipeFile, debug.CrashOptions{}); err != nil {
		_ = pipeFile.Close()
		return fmt.Errorf("crashtracker: set crash output: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
		_ = pipeFile.Close()
		return fmt.Errorf("crashtracker: start monitor process: %w", err)
	}

	// SetCrashOutput duplicated the fd internally, so this write end can be
	// released; the monitor keeps running with its own duplicate.
	_ = pipeFile.Close()

	// Reap the child when it exits to release OS resources (fds, zombie on Linux).
	// Log non-zero exits: the monitor always calls os.Exit(0), so a non-zero
	// status indicates the monitor itself panicked during parse or upload.
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Warn("crashtracker: monitor process exited unexpectedly: %v", err.Error())
		}
	}()

	return nil
}

// newDiagnosticsForwarder returns two writers for a child's stdout and stderr.
// Each line the child writes is forwarded to the shared logger as a single
// log call, so concurrent writers never interleave partial lines on a shared fd.
func newDiagnosticsForwarder() (stdout, stderr io.Writer, err error) {
	stdout, err = newLineForwarder("stdout")
	if err != nil {
		return nil, nil, err
	}
	stderr, err = newLineForwarder("stderr")
	if err != nil {
		return nil, nil, err
	}
	return stdout, stderr, nil
}

// newLineForwarder returns the write end of a pipe whose lines are logged
// through internal/log, one log.Warn call per complete line. stream is a
// data argument (e.g. "stdout"/"stderr"), not a format string.
func newLineForwarder(stream string) (io.Writer, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create pipe: %w", err)
	}
	go func() {
		defer r.Close()
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			log.Warn("crashtracker: monitor %s: %s", stream, sc.Text())
		}
	}()
	return w, nil
}
