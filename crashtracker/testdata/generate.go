//go:build ignore

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// Generate captures crash-fd and stderr fixtures with Go 1.26:
//
//	GOTOOLCHAIN=go1.26.0 go run crashtracker/testdata/generate.go
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

func main() {
	if len(os.Args) == 4 && os.Args[1] == "crash" {
		crash(os.Args[2], os.Args[3])
		return
	}
	if runtime.Version() != "go1.26.0" {
		panic("fixtures require go1.26.0")
	}
	exe, err := os.Executable()
	must(err)
	tmp, err := os.MkdirTemp("", "crashtracker-fixtures-")
	must(err)
	defer os.RemoveAll(tmp)
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		panic("cannot locate generator source")
	}
	paths := strings.NewReplacer(runtime.GOROOT(), "/usr/local/go", filepath.Dir(source), "/app")
	for _, name := range []string{
		"concurrent_map_write", "concurrent_map_read_write", "deadlock",
		"stack_exhaustion", "close_closed_channel", "nil_map_write", "nil_deref",
	} {
		base := filepath.Join("crashtracker", "testdata", name)
		rawFD := filepath.Join(tmp, name+".txt")
		var stderr bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		cmd := exec.CommandContext(ctx, exe, "crash", name, rawFD)
		cmd.Stderr = &stderr
		err = cmd.Run()
		cancel()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
			panic(fmt.Sprintf("%s: expected crash exit 2, got %v", name, err))
		}
		fd, err := os.ReadFile(rawFD)
		must(err)
		must(os.WriteFile(base+"_fd_go126.txt", []byte(paths.Replace(string(fd))), 0o644))
		must(os.WriteFile(base+"_stderr_go126.txt", []byte(paths.Replace(stderr.String())), 0o644))
		fmt.Println(name + ": captured fd and stderr")
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func crash(name, path string) {
	f, err := os.Create(path)
	must(err)
	must(debug.SetCrashOutput(f, debug.CrashOptions{}))
	must(f.Close())
	debug.SetTraceback("all")
	runtime.GOMAXPROCS(2)
	switch name {
	case "concurrent_map_write":
		m := make(map[int]int)
		go writeMap(m)
		writeMap(m)
	case "concurrent_map_read_write":
		m := map[int]int{0: 0}
		go writeMap(m)
		for {
			_ = m[0]
		}
	case "deadlock":
		select {}
	case "stack_exhaustion":
		debug.SetMaxStack(32 * 1024)
		recurse()
	case "close_closed_channel":
		ch := make(chan struct{})
		close(ch)
		close(ch)
	case "nil_map_write":
		var m map[int]int
		m[0] = 1
	case "nil_deref":
		var p *int
		*p = 1
	default:
		panic("unknown crash: " + name)
	}
}

func writeMap(m map[int]int) {
	for {
		for i := range 1024 {
			m[i] = i
		}
	}
}

//go:noinline
func recurse() {
	recurse()
}
