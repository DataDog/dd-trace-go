// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
)

func supervisorNotifyRelay(cmd *exec.Cmd) (start func(appPID int), stop func(), err error) {
	const notifySocket = "NOTIFY_SOCKET"
	address := env.Get(notifySocket)
	if address == "" {
		return func(int) {}, func() {}, nil
	}
	if address[0] != '/' && address[0] != '@' {
		return nil, nil, errors.New("NOTIFY_SOCKET must start with / or @ for a Unix socket")
	}
	var suffix [8]byte
	rand.Read(suffix[:])
	name := fmt.Sprintf("@dd-ct-notify-%d-%x", os.Getpid(), suffix)
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: name, Net: "unixgram"})
	if err != nil {
		return nil, nil, err
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		socketErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_PASSCRED, 1)
	}); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	if socketErr != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("enable SO_PASSCRED: %w", socketErr)
	}
	cmd.Env = append(cmd.Env, notifySocket+"="+name)
	manager := &net.UnixAddr{Name: address, Net: "unixgram"}
	var done chan struct{}
	start = func(appPID int) {
		if done != nil {
			return
		}
		done = make(chan struct{})
		go func() {
			defer close(done)
			relayNotifications(conn, raw, manager, appPID)
		}()
	}
	stop = func() {
		if done != nil {
			// Stop new packets, then drain packets that the child sent before exit.
			_ = raw.Control(func(fd uintptr) { _ = unix.Shutdown(int(fd), unix.SHUT_RD) })
			_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
			_ = conn.SetReadDeadline(time.Now())
			<-done
		}
		_ = conn.Close()
	}
	return start, stop, nil
}

func relayNotifications(conn *net.UnixConn, raw syscall.RawConn, manager *net.UnixAddr, appPID int) {
	// Linux permits at most 253 descriptors in one datagram (SCM_MAX_FD).
	control := make([]byte, unix.CmsgSpace(unix.SizeofUcred)+unix.CmsgSpace(253*4))
	var payload []byte
	var n, oobn, flags int
	var readErr error
	receive := func(fd uintptr) {
		// Peek at the length without taking any descriptors.
		n, _, _, _, readErr = unix.Recvmsg(int(fd), nil, nil, unix.MSG_PEEK|unix.MSG_TRUNC)
		if readErr != nil {
			return
		}
		if n > cap(payload) {
			payload = make([]byte, n)
		}
		n, oobn, flags, _, readErr = unix.Recvmsg(int(fd), payload[:n], control, unix.MSG_CMSG_CLOEXEC)
	}
	forward := func() {
		var fds []int
		defer func() {
			for _, fd := range fds {
				_ = unix.Close(fd)
			}
		}()
		var cred *unix.Ucred
		for remaining := control[:oobn]; len(remaining) >= unix.CmsgLen(0); {
			header, data, rest, err := unix.ParseOneSocketControlMessage(remaining)
			if err != nil {
				return
			}
			remaining = rest
			if header.Level != unix.SOL_SOCKET {
				continue
			}
			msg := &unix.SocketControlMessage{Header: header, Data: data}
			switch header.Type {
			case unix.SCM_RIGHTS:
				rights, _ := unix.ParseUnixRights(msg)
				fds = append(fds, rights...)
			case unix.SCM_CREDENTIALS:
				cred, _ = unix.ParseUnixCredentials(msg)
			}
		}
		if flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 || cred == nil || int(cred.Pid) != appPID {
			return
		}
		data, mainPIDFD := stripNotifyMainPID(payload[:n])
		if len(data) != 0 {
			var rights []byte
			// systemd accepts a pidfd handoff only with exactly one descriptor.
			if len(fds) != 0 && !(mainPIDFD && len(fds) == 1) {
				rights = unix.UnixRights(fds...)
			}
			_, _, _ = conn.WriteMsgUnix(data, rights, manager)
		}
	}
	for {
		if err := raw.Read(func(fd uintptr) bool {
			receive(fd)
			return !errors.Is(readErr, unix.EAGAIN)
		}); err != nil {
			break
		}
		if errors.Is(readErr, unix.EINTR) {
			continue
		}
		if readErr != nil {
			return
		}
		forward()
	}
	// Read the remaining packets without waiting after the read deadline.
	for {
		if err := raw.Control(receive); err != nil {
			return
		}
		if errors.Is(readErr, unix.EINTR) {
			continue
		}
		if readErr != nil {
			return
		}
		forward()
	}
}

func stripNotifyMainPID(data []byte) ([]byte, bool) {
	out := data[:0]
	var mainPIDFD bool
	for len(data) != 0 {
		end := bytes.IndexAny(data, "\r\n") + 1
		if end == 0 {
			end = len(data)
		} else if end < len(data) && data[end-1] == '\r' && data[end] == '\n' {
			end++
		}
		line := data[:end]
		switch {
		case bytes.HasPrefix(line, []byte("MAINPID=")):
		case bytes.Equal(bytes.TrimRight(line, "\r\n"), []byte("MAINPIDFD=1")):
			mainPIDFD = true
		default:
			out = append(out, line...)
		}
		data = data[end:]
	}
	return out, mainPIDFD
}
