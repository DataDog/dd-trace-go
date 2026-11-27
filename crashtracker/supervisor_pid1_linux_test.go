// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/DataDog/dd-trace-go/v2/crashtracker"
)

const (
	pid1HelperEnv = "_CRASHTRACKER_PID1_HELPER"
	pid1TestRun   = "-test.run=^TestSupervisorReapsOrphans$"
	pid1ChildExit = 23
)

func TestSupervisorReapsOrphans(t *testing.T) {
	switch os.Getenv(pid1HelperEnv) {
	case "supervisor":
		_, supervised := helperMarkerFD()
		if !supervised && os.Getpid() != 1 {
			t.Fatalf("supervisor PID = %d, want 1", os.Getpid())
		}
		if err := crashtracker.Start(); err != nil {
			t.Fatal(err)
		}
		if !supervised || os.Getppid() != 1 {
			t.Fatalf("application does not have a supervisor at PID 1")
		}
		cmd := pid1HelperCommand(t.Context(), "parent")
		cmd.Stderr = os.Stderr
		output, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(output)))
		if err != nil || pid <= 1 {
			t.Fatalf("invalid orphan PID %q: %v", output, err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			err := unix.Kill(pid, 0)
			if errors.Is(err, unix.ESRCH) {
				os.Exit(pid1ChildExit)
			}
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("orphan PID %d still exists after its parent exited", pid)
	case "parent":
		cmd := pid1HelperCommand(t.Context(), "orphan")
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		fmt.Println(cmd.Process.Pid)
		os.Exit(0)
	case "orphan":
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if os.Getppid() == 1 {
				os.Exit(0)
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("orphan did not get reparented to PID 1")
	case "":
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		cmd := pid1HelperCommand(ctx, "supervisor")
		cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: unix.CLONE_NEWPID}
		output, err := cmd.CombinedOutput()
		if errors.Is(err, unix.EPERM) {
			cmd := pid1HelperCommand(ctx, "supervisor")
			cmd.SysProcAttr = &syscall.SysProcAttr{
				Cloneflags:                 unix.CLONE_NEWUSER | unix.CLONE_NEWPID,
				UidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}},
				GidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}},
				GidMappingsEnableSetgroups: false,
			}
			output, err = cmd.CombinedOutput()
			if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSPC) {
				t.Skipf("cannot create a PID namespace: %v. Run the test binary as container PID 1 with %s=supervisor", err, pid1HelperEnv)
			}
		}
		if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != pid1ChildExit {
			t.Fatalf("supervisor exit = %v, want exit status %d\n%s", err, pid1ChildExit, output)
		}
	default:
		t.Fatalf("unknown helper mode %q", os.Getenv(pid1HelperEnv))
	}
}

func pid1HelperCommand(ctx context.Context, mode string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], pid1TestRun)
	cmd.Env = append(filterE2EEnv(os.Environ()), pid1HelperEnv+"="+mode, "DD_CRASHTRACKING_ENABLED=true", "GORACE=atexit_sleep_ms=0")
	return cmd
}
