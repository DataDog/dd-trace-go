// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	// Unix appends the crash fd after inherited descriptors in ExtraFiles.
	// Windows passes an inherited handle.
	// The marker holds "<supervisor pid>:<value>" and exists only until the
	// child's Start. The child takes the child role only when the PID equals
	// its parent PID, so a stray inherited marker does not skip supervision.
	// The leading underscore marks it as internal, like _DD_ROOT_GO_SESSION_ID,
	// so it stays out of the supported configuration registry.
	supervisedFDEnv = "_DD_CRASHTRACKING_SUPERVISED_FD"

	// firstExtraFD is the descriptor of exec.Cmd.ExtraFiles[0].
	// ExtraFiles[i] becomes fd firstExtraFD+i in the child.
	firstExtraFD = 3

	maxCrashDumpSize = 32 * 1024 * 1024 // 32 MiB
	// supervisorMemoryLimit is the supervisor's GOMEMLIMIT. Parsing a dump at
	// maxCrashDumpSize peaks at about 124 MiB above a baseline that already
	// holds the dump (see manygoroutines_measure_test.go).
	supervisorMemoryLimit = 256 * 1024 * 1024
	// defaultGCPercent is the supervisor's GOGC, which is the Go default.
	defaultGCPercent = 100

	// uploadDeadline bounds the whole upload phase, including retries.
	// After a crash the supervisor exits up to pipeEOFTimeout plus
	// uploadDeadline after the child exits.
	uploadDeadline = 10 * time.Second
	// attemptTimeout follows libdatadog RFC 0003 for Agent uploads.
	// Three seconds leaves time to retry within uploadDeadline.
	attemptTimeout = 3 * time.Second
	// agentlessAttemptTimeout gives HTTPS uploads more time over a wide area network.
	// After a 5-second attempt and a 500 ms backoff, uploadDeadline allows up to 4.5 seconds to retry.
	agentlessAttemptTimeout = 5 * time.Second

	// pipeEOFTimeout bounds the wait for the crash pipe to reach EOF. A
	// descendant of the application can hold the write end open.
	pipeEOFTimeout = 2 * time.Second
)

// crashDump is the result of reading the crash pipe.
type crashDump struct {
	data      []byte
	truncated bool
	err       error
}

func registerCrashOutput(value string) error {
	fd, err := strconv.ParseUint(value, 10, strconv.IntSize)
	if err != nil || fd < firstExtraFD {
		return fmt.Errorf("crashtracker: invalid inherited crash descriptor %q (fd numbers on Unix, handles on Windows)", value)
	}
	f, err := inheritedCrashPipe(uintptr(fd))
	if err != nil {
		return fmt.Errorf("crashtracker: validate inherited crash pipe: %w", err)
	}
	defer f.Close()
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		return fmt.Errorf("crashtracker: set crash output: %w", err)
	}
	return nil
}

// readCrashDump looks one byte past the cap to distinguish truncation from EOF.
func readCrashDump(r io.Reader) (data []byte, truncated bool, err error) {
	data, err = io.ReadAll(io.LimitReader(r, maxCrashDumpSize+1))
	if err != nil {
		return data, false, err
	}
	truncated = len(data) > maxCrashDumpSize
	if truncated {
		data = data[:maxCrashDumpSize]
	}
	return data, truncated, nil
}

// applySupervisorRuntimeLimits overrides GOMEMLIMIT and GOGC, which the
// application sets for its own workload.
func applySupervisorRuntimeLimits() {
	debug.SetMemoryLimit(supervisorMemoryLimit)
	debug.SetGCPercent(defaultGCPercent)
}

func supervise(cfg *config) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("crashtracker: get executable path: %w", err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("crashtracker: create crash pipe: %w", err)
	}
	defer r.Close()
	defer w.Close()

	cmd := exec.Command(exe, os.Args[1:]...) //nolint:gosec // The application re-executes its own binary.
	cmd.Args[0] = os.Args[0]
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	terminalMode := supervisorTerminalMode()
	pipeFD, err := configureChild(cmd, w, terminalMode)
	if err != nil {
		return fmt.Errorf("crashtracker: configure application process: %w", err)
	}
	cmd.Env = append(os.Environ(), supervisedFDEnv+"="+strconv.Itoa(os.Getpid())+":"+pipeFD)
	startForwarding, stopForwarding := supervisorSignals(terminalMode)
	releaseThread := retainSupervisorThread(cmd)
	err = startChild(cmd)
	for _, f := range cmd.ExtraFiles {
		if f != nil && f != w {
			_ = f.Close()
		}
	}
	if err != nil {
		releaseThread()
		stopForwarding()
		return fmt.Errorf("crashtracker: start application process: %w", err)
	}
	closeInheritedDescriptors(cmd, w)
	startForwarding(cmd.Process)
	_ = w.Close()
	applySupervisorRuntimeLimits()

	dumps := make(chan crashDump, 1)
	go func() {
		data, truncated, err := readCrashDump(r)
		// Keep reading past the cap so a large runtime dump cannot block exit.
		_, _ = io.Copy(io.Discard, r)
		dumps <- crashDump{data, truncated, err}
	}()
	waitErr := cmd.Wait()
	_, exited := errors.AsType[*exec.ExitError](waitErr)
	if cmd.ProcessState == nil || (waitErr != nil && !exited) {
		reason := "missing process state"
		if waitErr != nil {
			reason = waitErr.Error()
		}
		log.Warn("crashtracker: wait for application process failed: %s", reason)
		// The child status is unknown, but a crash dump can already be in the pipe.
		finishCrashReport(cfg, r, dumps)
		stopForwarding()
		os.Exit(1)
	}
	finishCrashReport(cfg, r, dumps)
	stopForwarding()
	exitWithChildStatus(cmd.ProcessState)
	// exitWithChildStatus never returns; this return satisfies the compiler.
	return nil
}

// finishCrashReport waits for the crash pipe to reach EOF, then uploads the dump it read.
// It takes up to pipeEOFTimeout plus uploadDeadline.
// The upload ignores stop signals: the child is gone and the report is what the supervisor is for.
func finishCrashReport(cfg *config, r *os.File, dumps <-chan crashDump) {
	var dump crashDump
	select {
	case dump = <-dumps:
	case <-time.After(pipeEOFTimeout):
		log.Warn("crashtracker: timed out waiting for crash pipe EOF")
		// The runtime wrote the whole dump before the child exited. Closing the
		// pipe unblocks the reader, which returns the bytes it already has.
		_ = r.Close()
		select {
		case dump = <-dumps:
			// Our own Close caused this error. The bytes read so far are the dump.
			if errors.Is(dump.err, os.ErrClosed) {
				dump.err = nil
			}
		case <-time.After(100 * time.Millisecond):
			log.Warn("crashtracker: discarded crash report: the reader did not return after closing the pipe")
		}
	}
	_ = r.Close()
	if dump.err != nil {
		log.Warn("crashtracker: crash pipe read failed: %v", dump.err.Error()) //errtrack:ignore pipe read failure
		return
	}
	if len(dump.data) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), uploadDeadline)
	defer cancel()
	if err := uploadReport(ctx, cfg, buildCrashReport(cfg, dump)); err != nil {
		log.Warn("crashtracker: upload failed: %v", err.Error()) //errtrack:ignore remote upload failure
	} else {
		log.Info("crashtracker: upload succeeded")
	}
}

// buildCrashReport parses the dump and marks the report incomplete when the dump was capped.
func buildCrashReport(cfg *config, dump crashDump) *Report {
	report := parseCrashDump(dump.data)
	if dump.truncated && report.Error.Stack != nil {
		report.Error.Stack.Incomplete = true
	}
	report.DDTags = buildDDTags(cfg, report)
	return report
}
