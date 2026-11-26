// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build !windows

package crashtracker

import (
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/unix"
)

func TestTerminalMode(t *testing.T) {
	for _, tt := range []struct {
		name   string
		hasTTY bool
		pid    int
		ppid   int
		want   bool
	}{
		{"container init", true, 1, 40, false},
		{"init parent", true, 50, 1, false},
		{"parent outside namespace", true, 50, 0, false},
		{"no terminal", false, 50, 40, false},
		{"shell job", true, 50, 40, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTerminalMode(tt.hasTTY, tt.pid, tt.ppid); got != tt.want {
				t.Fatalf("isTerminalMode(%t, %d, %d) = %t, want %t", tt.hasTTY, tt.pid, tt.ppid, got, tt.want)
			}
		})
	}
}

func TestConfigureChildSkipsCloseOnExecDirectory(t *testing.T) {
	dir, err := os.Open("/dev/fd")
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	fd := dir.Fd()
	flags, err := unix.FcntlInt(fd, unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("directory flags = %d, error = %v, want FD_CLOEXEC", flags, err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	cmd := &exec.Cmd{}
	if _, err := configureChild(cmd, w, false); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, f := range cmd.ExtraFiles {
			if f != nil && f != w {
				_ = f.Close()
			}
		}
	}()
	if i := int(fd) - 3; i >= 0 && i < len(cmd.ExtraFiles) && cmd.ExtraFiles[i] != nil && cmd.ExtraFiles[i] != w {
		t.Fatal("child inherits the close-on-exec directory")
	}
}
