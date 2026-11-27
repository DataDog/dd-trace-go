// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestStripNotifyMainPID(t *testing.T) {
	for _, tt := range []struct {
		name      string
		input     string
		want      string
		mainPIDFD bool
	}{
		{name: "last line without newline", input: "READY=1\nMAINPID=123", want: "READY=1\n"},
		{name: "CRLF", input: "READY=1\r\nMAINPID=123\r\nMAINPIDFD=1\r\nSTATUS=ready\r\n", want: "READY=1\r\nSTATUS=ready\r\n", mainPIDFD: true},
		{name: "different key", input: "XMAINPID=123\n", want: "XMAINPID=123\n"},
		{name: "pidfd without newline", input: "READY=1\nMAINPIDFD=1", want: "READY=1\n", mainPIDFD: true},
		{name: "only main PID", input: "MAINPID=123"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, mainPIDFD := stripNotifyMainPID([]byte(tt.input))
			if string(got) != tt.want || mainPIDFD != tt.mainPIDFD {
				t.Errorf("stripNotifyMainPID(%q) = (%q, %t), want (%q, %t)", tt.input, got, mainPIDFD, tt.want, tt.mainPIDFD)
			}
		})
	}
}

func TestSupervisorNotifyRelayRejectsNonUnixAddress(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "vsock:2:1234")
	var cmd exec.Cmd
	_, stop, err := supervisorNotifyRelay(&cmd)
	if stop != nil {
		t.Cleanup(stop)
	}
	if err == nil || !strings.Contains(err.Error(), "NOTIFY_SOCKET") {
		t.Fatalf("supervisorNotifyRelay error = %v, want a NOTIFY_SOCKET error", err)
	}
	if len(cmd.Env) != 0 {
		t.Errorf("supervisorNotifyRelay changed the child environment: %v", cmd.Env)
	}
}

func TestSupervisorNotifyRelayAcceptsApplicationPID(t *testing.T) {
	manager, send := newNotifyRelayTestSockets(t, os.Getpid())
	const message = "READY=1"
	send(message, nil)
	if err := manager.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var payload [32]byte
	n, err := manager.Read(payload[:])
	if err != nil || string(payload[:n]) != message {
		t.Fatalf("read application notification = %q, %v, want %q", payload[:n], err, message)
	}
}

func TestSupervisorNotifyRelayRejectsOtherPID(t *testing.T) {
	manager, send := newNotifyRelayTestSockets(t, os.Getpid()+1)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	send("READY=1", unix.UnixRights(int(w.Fd())))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var probe [1]byte
	if _, err := r.Read(probe[:]); !errors.Is(err, io.EOF) {
		t.Fatalf("read rejected descriptor pipe: %v, want EOF", err)
	}
	if err := manager.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Read(probe[:]); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("read rejected notification: %v, want a timeout", err)
	}
}

func newNotifyRelayTestSockets(t *testing.T, appPID int) (*net.UnixConn, func(string, []byte)) {
	t.Helper()
	managerAddr := &net.UnixAddr{Name: filepath.Join(t.TempDir(), "notify"), Net: "unixgram"}
	manager, err := net.ListenUnixgram("unixgram", managerAddr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	t.Setenv("NOTIFY_SOCKET", managerAddr.Name)
	missing := filepath.Join(t.TempDir(), "missing")
	t.Setenv("TMPDIR", missing)

	var cmd exec.Cmd
	start, stop, err := supervisorNotifyRelay(&cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	if len(cmd.Env) != 1 || !strings.HasPrefix(cmd.Env[0], "NOTIFY_SOCKET=@") {
		t.Fatalf("child environment = %q, want an abstract NOTIFY_SOCKET", cmd.Env)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat TMPDIR: %v, want a missing directory", err)
	}
	start(appPID)
	addr := &unix.SockaddrUnix{Name: strings.TrimPrefix(cmd.Env[0], "NOTIFY_SOCKET=")}
	sender, err := unix.Socket(unix.AF_UNIX, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(sender) })
	return manager, func(message string, control []byte) {
		t.Helper()
		if _, err := unix.SendmsgN(sender, []byte(message), control, addr, 0); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSupervisorNotifyRelayStopBeforeStart(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "@unused")
	var cmd exec.Cmd
	_, stop, err := supervisorNotifyRelay(&cmd)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stop blocked before start")
	}
}
