// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package profiler

import (
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGoroutineLeakProfile(t *testing.T) {
	if !strings.HasPrefix(runtime.Version(), "go1.26.") {
		// This is experimental in Go 1.26. We'll need to revisit this
		// code when Go 1.27 is released.
		t.Skipf("goroutineleakprofile requires Go 1.26, got %s", runtime.Version())
	}

	t.Run("with_experiment", func(t *testing.T) {
		meta := runGoroutineLeakProgram(t, true)
		if _, ok := meta.attachments["goroutineleak.pprof"]; !ok {
			t.Errorf("expected goroutineleak.pprof attachment, got: %v", meta.event.Attachments)
		}
	})

	t.Run("without_experiment", func(t *testing.T) {
		meta := runGoroutineLeakProgram(t, false)
		if _, ok := meta.attachments["goroutineleak.pprof"]; ok {
			t.Errorf("unexpected goroutineleak.pprof attachment without GOEXPERIMENT")
		}
	})
}

func runGoroutineLeakProgram(t *testing.T, withExperiment bool) profileMeta {
	t.Helper()
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(goroutineLeakSource), 0644); err != nil {
		t.Fatalf("writing test source: %s", err)
	}

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("getting repo root: %s", err)
	}

	for _, cmd := range []*exec.Cmd{
		exec.Command("go", "mod", "init", "goroutineleak_test_app"),
		exec.Command("go", "mod", "edit",
			"-require=github.com/DataDog/dd-trace-go/v2@v2.0.0",
			"-replace=github.com/DataDog/dd-trace-go/v2@v2.0.0="+repoRoot,
		),
		exec.Command("go", "mod", "tidy"),
	} {
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %s", cmd.String(), out)
		}
	}

	binPath := filepath.Join(dir, "app")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOWORK=off")
	if withExperiment {
		build.Env = append(build.Env, "GOEXPERIMENT=goroutineleakprofile")
	}
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%s: %s", build.String(), out)
	}

	backend := &fakeBackend{profiles: make(chan profileMeta, 1)}
	srv := httptest.NewServer(backend)
	t.Cleanup(srv.Close)

	cmd := exec.Command(binPath)
	cmd.Env = []string{fmt.Sprintf("DD_TRACE_AGENT_URL=%s", srv.URL)}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting test program: %s", err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})

	p := <-backend.profiles
	if p.err != nil {
		t.Fatalf("profile upload error: %s", p.err)
	}
	return p
}

const goroutineLeakSource = `package main

import (
	"time"

	"github.com/DataDog/dd-trace-go/v2/profiler"
)

func main() {
	err := profiler.Start(
		profiler.WithProfileTypes(), // only leak profile matters; auto-enabled if available
		profiler.WithPeriod(10*time.Millisecond),
	)
	if err != nil {
		panic(err)
	}
	defer profiler.Stop()

	// Run until killed. This has the side effect of leaking a goroutine in
	// case we care about checking for a non-empty profile.
	select {}
}
`
