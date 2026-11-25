// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"errors"
	"os"
	"os/signal"
	"unsafe"

	"golang.org/x/sys/unix"
)

func supervisorReapOrphans(appPID int) func() {
	if os.Getpid() != 1 {
		return func() {}
	}
	exits := make(chan os.Signal, 1)
	signal.Notify(exits, unix.SIGCHLD)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		// Check once for exits that occurred before signal.Notify.
		for {
			for reapOrphan(appPID) {
				select {
				case <-stop:
					return
				default:
				}
			}
			select {
			case <-stop:
				return
			case <-exits:
			}
		}
	}()
	return func() {
		signal.Stop(exits)
		close(stop)
		<-done
	}
}

// waitidSiginfo has the Linux siginfo layout for SIGCHLD. The fields after the
// three int32 header fields start at offset 16 on 64-bit and 12 on 32-bit.
type waitidSiginfo struct {
	_   [3 + (^uint(0) >> 63)]int32
	pid int32
	_   [112 - (^uint(0)>>63)*4]byte
}

func reapOrphan(appPID int) bool {
	var info waitidSiginfo
	err := unix.Waitid(unix.P_ALL, 0, (*unix.Siginfo)(unsafe.Pointer(&info)), unix.WEXITED|unix.WNOWAIT|unix.WNOHANG, nil)
	if errors.Is(err, unix.EINTR) {
		return true
	}
	pid := int(info.pid)
	// Only cmd.Wait can collect the application's exit status.
	if err != nil || pid <= 0 || pid == appPID {
		return false
	}
	_, err = unix.Wait4(pid, nil, unix.WNOHANG, nil)
	return err == nil || errors.Is(err, unix.EINTR)
}
