//go:build ignore

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// Generate captures crash-fd fixtures, and stderr fixtures for the kinds that
// need them, with Go 1.26. Run it from the repository root with Go 1.26 on
// Linux, for example:
//
//	docker run --rm -v "$PWD":/src -w /src golang:1.26 go run crashtracker/testdata/generate.go
//
// Name kinds as arguments to capture only those kinds.
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
	"slices"
	"strings"
	"sync"
	"time"
)

func main() {
	if len(os.Args) == 4 && os.Args[1] == "crash" {
		crash(os.Args[2], os.Args[3])
		return
	}
	if !strings.HasPrefix(runtime.Version(), "go1.26") {
		panic("fixtures require Go 1.26")
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
	wanted := os.Args[1:]
	for _, kind := range kinds {
		name := kind.name
		if len(wanted) > 0 && !slices.Contains(wanted, name) {
			continue
		}
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
		if kind.stderr {
			must(os.WriteFile(base+"_stderr_go126.txt", []byte(paths.Replace(stderr.String())), 0o644))
		}
		fmt.Println(name + ": captured")
	}
}

// kinds lists every crash kind. The stderr flag marks the kinds whose stderr
// text the tests also read, because the runtime omits it from the crash file
// (https://github.com/golang/go/issues/76859).
var kinds = []struct {
	name   string
	stderr bool
}{
	{"concurrent_map_write", true}, {"concurrent_map_read_write", true}, {"deadlock", true},
	{"stack_exhaustion", true}, {"close_closed_channel", true}, {"nil_map_write", true}, {"nil_deref", true},
	{"mutex_unlock", true}, {"rwmutex_runlock", true},
	{"repanic", false}, {"panic_struct", false}, {"panic_string", false}, {"panic_error", false},
	{"panic_wrapped_error", false}, {"panic_custom_error", false}, {"panic_int", false},
	{"panic_nil", false}, {"index_out_of_range", false}, {"send_closed_channel", false},
	{"type_assertion", false}, {"divide_by_zero", false}, {"panic_goroutine", false},
}

type point struct{ x, y int }

type customError struct{ code int }

func (e customError) Error() string { return fmt.Sprintf("custom error %d", e.code) }

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
	case "mutex_unlock":
		var mu sync.Mutex
		mu.Unlock()
	case "rwmutex_runlock":
		var mu sync.RWMutex
		mu.RUnlock()
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
	case "repanic":
		defer func() {
			panic("repanic after: " + fmt.Sprint(recover()))
		}()
		panic("first")
	case "panic_struct":
		panic(point{1, 2})
	case "panic_string":
		panic("boom")
	case "panic_error":
		panic(errors.New("boom error"))
	case "panic_wrapped_error":
		panic(fmt.Errorf("wrapped: %w", errors.New("inner error")))
	case "panic_custom_error":
		panic(customError{code: 7})
	case "panic_int":
		panic(42)
	case "panic_nil":
		panic(nil)
	case "index_out_of_range":
		s := []int{1, 2, 3}
		i := len(name) + 100
		_ = s[i]
	case "send_closed_channel":
		ch := make(chan int, 1)
		close(ch)
		ch <- 1
	case "type_assertion":
		var v any = "text"
		_ = v.(int)
	case "divide_by_zero":
		zero := len(name) - len(name)
		_ = 1 / zero
	case "panic_goroutine":
		go func() { panic("goroutine boom") }()
		select {}
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
