// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fuzzfixture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestFixtureChildProcess(t *testing.T) {
	switch os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") {
	case "command-exit":
		os.Exit(17)
	case "command-success":
		fmt.Println("FIXTURE_CHILD_SUCCESS")
		os.Exit(0)
	case "command-block":
		fmt.Println("FIXTURE_CHILD_READY")
		select {}
	default:
		t.Skip("subprocess entrypoint")
	}
}

func TestFixtureChildExit(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"command-exit", "command-success"} {
		output, err := runFixtureChild(binary, append(os.Environ(), "DD_FUZZ_EXAMPLE_SCENARIO="+scenario), "-test.run=^TestFixtureChildProcess$")
		if scenario == "command-exit" {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 17 {
				t.Fatalf("exit=%v, want 17: %s", err, output)
			}
		} else if err != nil || !strings.Contains(string(output), "FIXTURE_CHILD_SUCCESS") {
			t.Fatalf("success child: %v: %s", err, output)
		}
	}
}

func TestFixtureChildCancellation(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The deadline is only a watchdog; the ready message triggers cancellation.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestFixtureChildProcess$", "-test.timeout=2m")
	cmd.Env = append(os.Environ(), "DD_FUZZ_EXAMPLE_SCENARIO=command-block", "GOTRACEBACK=all")
	output := &cancelOnReady{cancel: cancel}
	cmd.Stdout, cmd.Stderr = output, output
	err = runChildCommand(ctx, cmd)
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "TestFixtureChildProcess") {
		t.Fatalf("cancellation lost: %v: %s", err, output.String())
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		t.Fatalf("cancellation can be mistaken for an expected exit: %v", err)
	}
	if cmd.ProcessState == nil || !strings.Contains(output.String(), "FIXTURE_CHILD_READY") {
		t.Fatalf("child was not started and reaped: %s", output.String())
	}
	if runtime.GOOS != "windows" && (!strings.Contains(output.String(), "SIGQUIT") || !strings.Contains(output.String(), "goroutine")) {
		t.Fatalf("missing child stack on cancellation: %s", output.String())
	}
	// A canceled child must not poison a subsequent invocation's context.
	result, err := runFixtureChild(binary, append(os.Environ(), "DD_FUZZ_EXAMPLE_SCENARIO=command-success"), "-test.run=^TestFixtureChildProcess$")
	if err != nil || !strings.Contains(string(result), "FIXTURE_CHILD_SUCCESS") {
		t.Fatalf("subsequent child: %v: %s", err, result)
	}
}

type cancelOnReady struct {
	buffer bytes.Buffer
	cancel context.CancelFunc
}

func (output *cancelOnReady) Write(p []byte) (int, error) {
	n, err := output.buffer.Write(p)
	if strings.Contains(output.String(), "FIXTURE_CHILD_READY") {
		output.cancel()
	}
	return n, err
}

func (output *cancelOnReady) String() string {
	return output.buffer.String()
}

func TestFixtureChildDeadline(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		time.Sleep(time.Second) // Virtual time; no subprocess or wall-clock wait.
		synctest.Wait()
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestFixtureChildProcess$")
		err := runChildCommand(ctx, cmd)
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "TestFixtureChildProcess") {
			t.Fatalf("deadline lost: %v", err)
		}
		if cmd.Process != nil {
			t.Fatal("expired context started a child")
		}
	})
}
