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
	// Unix passes fd 3 through ExtraFiles. Windows passes an inherited handle.
	// The marker holds its decimal value and exists only until the child's Start.
	// The leading underscore marks it as internal, like _DD_ROOT_GO_SESSION_ID,
	// so it stays out of the supported configuration registry.
	supervisedFDEnv = "_DD_CRASHTRACKING_SUPERVISED_FD"

	maxCrashDumpSize      = 32 * 1024 * 1024 // 32 MiB
	supervisorMemoryLimit = 256 * 1024 * 1024
)

func registerCrashOutput(value string) error {
	fd, err := strconv.ParseUint(value, 10, strconv.IntSize)
	if err != nil || fd < 3 {
		return fmt.Errorf("crashtracker: invalid inherited crash descriptor %q", value)
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
	marker, err := configureChild(cmd, w)
	if err != nil {
		return fmt.Errorf("crashtracker: configure application process: %w", err)
	}
	cmd.Env = append(os.Environ(), supervisedFDEnv+"="+marker)
	startForwarding, stopForwarding := supervisorSignals()
	releaseThread := retainSupervisorThread(cmd)
	if err := cmd.Start(); err != nil {
		releaseThread()
		stopForwarding()
		return fmt.Errorf("crashtracker: start application process: %w", err)
	}
	startForwarding(cmd.Process)
	_ = w.Close()
	debug.SetMemoryLimit(supervisorMemoryLimit)

	type crashDump struct {
		data      []byte
		truncated bool
		err       error
	}
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
		stopForwarding()
		os.Exit(1)
	}
	var dump crashDump
	select {
	case dump = <-dumps:
	case <-time.After(2 * time.Second):
		log.Warn("crashtracker: timed out waiting for crash pipe EOF")
	}
	_ = r.Close()
	if dump.err == nil && len(dump.data) != 0 {
		ctx, cancel := context.WithTimeout(context.Background(), uploadTimeout)
		report := parseCrashDump(dump.data)
		if dump.truncated && report.Error.Stack != nil {
			report.Error.Stack.Incomplete = true
		}
		report.DDTags = buildDDTags(cfg, report)
		if err := uploadReport(ctx, cfg, report); err != nil {
			log.Warn("crashtracker: upload failed: %v", err.Error()) //errtrack:ignore remote upload failure
		} else {
			log.Warn("crashtracker: upload succeeded")
		}
		cancel()
	}
	stopForwarding()
	exitWithChildStatus(cmd.ProcessState)
	return nil
}
