// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker_test

import (
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/DataDog/dd-trace-go/v2/crashtracker"
)

const notifyRelayHelperEnv = "_CRASHTRACKER_NOTIFY_RELAY_HELPER"

func TestSupervisorNotifyRelay(t *testing.T) {
	if os.Getenv(notifyRelayHelperEnv) == "1" {
		runNotifyRelayApplication(t)
		return
	}
	for _, tt := range []struct{ name, addr string }{
		{"filesystem", filepath.Join(t.TempDir(), "notify")},
		{"abstract", "@dd-crashtracker-notify-test-" + strconv.Itoa(os.Getpid())},
	} {
		t.Run(tt.name, func(t *testing.T) {
			addr := tt.addr
			manager, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: addr, Net: "unixgram"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = manager.Close() })
			raw, err := manager.SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			var socketErr error
			if err := raw.Control(func(fd uintptr) {
				socketErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_PASSCRED, 1)
			}); err != nil {
				t.Fatal(err)
			}
			if socketErr != nil {
				t.Fatal(socketErr)
			}
			if err := manager.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestSupervisorNotifyRelay$")
			detachSupervisor(cmd)
			cmd.Env = append(filterE2EEnv(os.Environ()), notifyRelayHelperEnv+"=1", "DD_CRASHTRACKING_ENABLED=true", "GORACE=atexit_sleep_ms=0", "NOTIFY_SOCKET="+addr)
			cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			})
			for _, want := range []string{"READY=1\nSTATUS= ready \nWATCHDOG=1\n", "BARRIER=1", "STOPPING=1"} {
				var payload [256]byte
				control := make([]byte, unix.CmsgSpace(unix.SizeofUcred)+unix.CmsgSpace(4))
				n, oobn, flags, _, err := manager.ReadMsgUnix(payload[:], control)
				if err != nil {
					t.Fatalf("read %q: %v", want, err)
				}
				if flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
					t.Fatalf("truncated notification, flags %#x", flags)
				}
				msgs, err := unix.ParseSocketControlMessage(control[:oobn])
				if err != nil {
					t.Fatal(err)
				}
				var pid int
				var fds []*os.File
				for _, msg := range msgs {
					switch msg.Header.Type {
					case unix.SCM_CREDENTIALS:
						cred, err := unix.ParseUnixCredentials(&msg)
						if err != nil {
							t.Fatal(err)
						}
						pid = int(cred.Pid)
					case unix.SCM_RIGHTS:
						rights, err := unix.ParseUnixRights(&msg)
						if err != nil {
							t.Fatal(err)
						}
						for _, fd := range rights {
							f := os.NewFile(uintptr(fd), "notify-barrier")
							t.Cleanup(func() { _ = f.Close() })
							fds = append(fds, f)
						}
					}
				}
				if pid != cmd.Process.Pid {
					t.Errorf("sender PID = %d, want supervisor PID %d", pid, cmd.Process.Pid)
				}
				if got := string(payload[:n]); got != want {
					t.Errorf("notification = %q, want %q", got, want)
				}
				if want == "BARRIER=1" {
					if len(fds) != 1 {
						t.Fatalf("received %d file descriptors, want 1", len(fds))
					}
					if _, err := fds[0].WriteString("ack"); err != nil {
						t.Fatal(err)
					}
					if err := fds[0].Close(); err != nil {
						t.Fatal(err)
					}
				} else if len(fds) != 0 {
					t.Fatalf("received %d unexpected file descriptors", len(fds))
				}
			}
			if err := cmd.Wait(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func runNotifyRelayApplication(t *testing.T) {
	t.Helper()
	if err := crashtracker.Start(); err != nil {
		t.Fatal(err)
	}
	sock, err := unix.Socket(unix.AF_UNIX, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(sock)
	addr := &unix.SockaddrUnix{Name: os.Getenv("NOTIFY_SOCKET")}
	send := func(message string, control []byte) {
		t.Helper()
		if _, err := unix.SendmsgN(sock, []byte(message), control, addr, 0); err != nil {
			t.Fatal(err)
		}
	}
	send("MAINPID=7", nil)
	// EOF on the pipe confirms that the relay closes the descriptor for MAINPIDFD=1.
	pidfdRead, pidfdWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pidfdRead.Close()
	defer pidfdWrite.Close()
	send("READY=1\nMAINPIDFD=1\nMAINPID=7\nSTATUS= ready \nWATCHDOG=1\n", unix.UnixRights(int(pidfdWrite.Fd())))
	if err := pidfdWrite.Close(); err != nil {
		t.Fatal(err)
	}
	if err := pidfdRead.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var probe [1]byte
	if _, err := pidfdRead.Read(probe[:]); !errors.Is(err, io.EOF) {
		t.Fatalf("read MAINPIDFD pipe: %v, want EOF", err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	send("BARRIER=1", unix.UnixRights(int(w.Fd())))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var ack [4]byte
	n, err := io.ReadFull(r, ack[:])
	if !errors.Is(err, io.ErrUnexpectedEOF) || string(ack[:n]) != "ack" {
		t.Fatalf("read barrier pipe = %q, %v, want ack followed by EOF", ack[:n], err)
	}
	send("STOPPING=1", nil)
	os.Exit(0)
}
