// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// monitorEnvVar is the environment variable set on the monitor child process.
const monitorEnvVar = "DD_CRASHTRACKING_IS_MONITOR_PROCESS"

// maxCrashDumpSize is the maximum number of bytes read from the crash pipe. It
// bounds memory usage in the monitor for very large crash dumps.
const maxCrashDumpSize = 32 * 1024 * 1024 // 32 MiB

// isMonitorProcess reports whether the current process is the monitor child.
//
// It reads the marker with os.Getenv directly rather than through the env
// validation layer because this check runs before the tracer's configuration
// machinery is initialised.
func isMonitorProcess() bool {
	return os.Getenv(monitorEnvVar) == "1" //nolint:forbidigo
}

// runMonitor is the monitor-child entry point. It reads crash output from stdin,
// parses it, and uploads a report. It never returns.
func runMonitor(cfg *config) {
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
	report.DDTags = buildDDTags(cfg)
	if err := uploadReport(cfg, report); err != nil {
		// Emit one line to stderr so operators know a crash report was attempted
		// but failed — without this, the failure is invisible.
		fmt.Fprintf(os.Stderr, "datadog/crashtracker: upload failed: %v\n", err)
	}
	os.Exit(0)
}

// buildChildEnv builds the environment for the monitor child process. It copies
// the parent env, drops runtime-tuning variables that would misconfigure the
// lightweight monitor (see golang/go#73490), and sets the monitor marker.
func buildChildEnv() []string {
	parentEnv := os.Environ()
	childEnv := make([]string, 0, len(parentEnv)+1)
	for _, kv := range parentEnv {
		// GOMEMLIMIT and GOGC are tuned for the application's workload; applying
		// them to the monitor can starve it or trigger GC pathologies.
		if strings.HasPrefix(kv, "GOMEMLIMIT=") || strings.HasPrefix(kv, "GOGC=") {
			continue
		}
		childEnv = append(childEnv, kv)
	}
	childEnv = append(childEnv, monitorEnvVar+"=1")
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
	// Route the monitor's stdout/stderr to the application's stderr so any
	// diagnostics from the monitor are not swallowed.
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = buildChildEnv()

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

	// Reap the child when it exits to release OS resources (fds, zombie on Linux).
	// Log non-zero exits: the monitor always calls os.Exit(0), so a non-zero
	// status indicates the monitor itself panicked during parse or upload.
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Warn("crashtracker: monitor process exited unexpectedly: %v", err)
		}
	}()

	// SetCrashOutput duplicated the fd internally, so this write end can be
	// released. Retain a reference for Stop to close it.
	cfg.pipeWriteEnd = pipeFile
	activePipe.Store(pipeFile)
	return nil
}
