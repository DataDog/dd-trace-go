// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package profiler

import (
	"go/version"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var go127OrNewer = version.Compare(runtime.Version(), "go1.27") >= 0

func TestGoroutineLeakProfile(t *testing.T) {
	if version.Compare(runtime.Version(), "go1.26") < 0 {
		t.Skipf("goroutineleakprofile requires Go 1.26 or later")
	}

	t.Run("with_experiment", func(t *testing.T) {
		if go127OrNewer {
			t.Skip("goroutineleakprofile is no longer an experiment")
		}
		meta, _ := runGoroutineLeakProgram(t, true, false)
		if _, ok := meta.attachments["goroutineleak.pprof"]; !ok {
			t.Errorf("expected goroutineleak.pprof attachment, got: %v", meta.event.Attachments)
		}
	})

	t.Run("with_experiment_and_explicit_profile", func(t *testing.T) {
		if go127OrNewer {
			t.Skip("goroutineleakprofile is no longer an experiment")
		}
		meta, _ := runGoroutineLeakProgram(t, true, true)
		if _, ok := meta.attachments["goroutineleak.pprof"]; !ok {
			t.Errorf("expected goroutineleak.pprof attachment, got: %v", meta.event.Attachments)
		}
	})

	t.Run("explicit_without_experiment", func(t *testing.T) {
		meta, output := runGoroutineLeakProgram(t, false, true)
		if go127OrNewer {
			if _, ok := meta.attachments["goroutineleak.pprof"]; !ok {
				t.Errorf("expected goroutineleak.pprof attachment, got: %v", meta.event.Attachments)
			}
		} else {
			const want = "goroutine leak profile requires Go 1.27 or later, or GOEXPERIMENT=goroutineleakprofile"
			if !strings.Contains(output, want) {
				t.Errorf("unexpected profiler.Start error: %s", output)
			}
		}
	})

	t.Run("off", func(t *testing.T) {
		meta, _ := runGoroutineLeakProgram(t, false, false)
		if _, ok := meta.attachments["goroutineleak.pprof"]; ok {
			t.Errorf("unexpected goroutineleak.pprof attachment without GOEXPERIMENT")
		}
	})
}

func runGoroutineLeakProgram(t *testing.T, withExperiment, explicit bool) (profileMeta, string) {
	t.Helper()
	dir := t.TempDir()

	profileType := ""
	if explicit {
		profileType = "profiler.GoroutineLeakProfile"
	}
	source := strings.Replace(goroutineLeakSource, "PROFILE_TYPE", profileType, 1)
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(source), 0644); err != nil {
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

	if explicit && !withExperiment && version.Compare(runtime.Version(), "go1.27") < 0 {
		// We expect this configuration (explicitly enabled but built
		// for Go 1.26 and with no GOEXPERIMENT) to exit without
		// producing any profiles. The profile type is ignored, so the
		// test program needs to exit explicitly after Start returns.
		cmd := exec.Command(binPath)
		cmd.Env = []string{"GOROUTINE_LEAK_TEST_EXIT=1"}
		output, _ := cmd.CombinedOutput()
		return profileMeta{}, string(output)
	}

	backend := &fakeBackend{profiles: make(chan profileMeta, 1)}
	srv := httptest.NewServer(backend)
	t.Cleanup(srv.Close)

	cmd := exec.Command(binPath)
	cmd.Env = []string{"DD_TRACE_AGENT_URL=" + srv.URL}
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
	return p, ""
}

const goroutineLeakSource = `package main

import (
	"fmt"
	"os"
	"time"

	"github.com/DataDog/dd-trace-go/v2/profiler"
)

func main() {
	err := profiler.Start(
		profiler.WithProfileTypes(PROFILE_TYPE), // only leak profile matters
		profiler.WithPeriod(10*time.Millisecond),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer profiler.Stop()

	if os.Getenv("GOROUTINE_LEAK_TEST_EXIT") != "" {
		return
	}

	// Run until killed. This has the side effect of leaking a goroutine in
	// case we care about checking for a non-empty profile.
	select {}
}
`
