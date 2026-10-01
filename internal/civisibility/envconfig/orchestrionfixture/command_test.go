// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package orchestrionfixture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func commandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 5 * time.Second
	configureCommandCancellation(cmd)
	return cmd
}

func TestCommandCancellation(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("process tree cancellation is tested on CI platforms")
	}
	for _, role := range []string{"parent", "deadline"} {
		t.Run(role, func(t *testing.T) {
			testCommandCancellation(t, role)
		})
	}
}

func testCommandCancellation(t *testing.T, role string) {
	t.Helper()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := listener.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	cmd := commandContext(ctx, os.Args[0], "-test.run=^TestCommandHelperProcess$", "-test.timeout=30s")
	cmd.Env = append(clientEnv(), "FIXTURE_COMMAND_ROLE="+role, "FIXTURE_COMMAND_ADDRESS="+listener.Addr().String())
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var commandErr error
	go func() {
		commandErr = cmd.Wait()
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()
	conn, err := listener.AcceptTCP()
	if err != nil {
		t.Fatalf("child did not connect: %v", err)
	}
	// Closing the connection also releases the child if cancellation is broken.
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(40 * time.Second)); err != nil {
		t.Fatal(err)
	}
	ready := make([]byte, len("ready\n"))
	if _, err := io.ReadFull(conn, ready); err != nil || string(ready) != "ready\n" {
		t.Fatalf("child did not become ready: %q, %v", ready, err)
	}
	if role == "parent" {
		cancel()
	}
	<-done
	if commandErr == nil {
		t.Fatalf("cancelled command succeeded:\n%s", output.String())
	}
	var b [1]byte
	// Terminating the child can close or reset its socket, depending on the OS.
	n, err := conn.Read(b[:])
	var netErr net.Error
	if n != 0 || err == nil || (errors.As(err, &netErr) && netErr.Timeout()) {
		t.Fatalf("child survived cancellation: read %d bytes, %v\n%s", n, err, output.String())
	}
	if role == "deadline" && !bytes.Contains(output.Bytes(), []byte("context: context deadline exceeded")) {
		t.Fatalf("command was not cancelled before the global test timeout:\n%s", output.String())
	}
}

func TestCommandHelperProcess(t *testing.T) {
	switch os.Getenv("FIXTURE_COMMAND_ROLE") {
	case "deadline":
		env := append(os.Environ(), "FIXTURE_COMMAND_ROLE=parent")
		runCommand(t, "", env, time.Minute, os.Args[0], "-test.run=^TestCommandHelperProcess$")
	case "parent":
		cmd := exec.Command(os.Args[0], "-test.run=^TestCommandHelperProcess$")
		cmd.Env = append(os.Environ(), "FIXTURE_COMMAND_ROLE=child")
		// Keep the parent's output pipes open, as Go compiler children do.
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	case "child":
		conn, err := net.DialTimeout("tcp", os.Getenv("FIXTURE_COMMAND_ADDRESS"), 10*time.Second)
		if err != nil {
			os.Exit(1)
		}
		defer conn.Close()
		if _, err := fmt.Fprintln(conn, "ready"); err != nil {
			os.Exit(1)
		}
		_, _ = io.Copy(io.Discard, conn)
		os.Exit(0)
	}
}
