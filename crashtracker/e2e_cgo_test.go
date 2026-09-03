// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestE2ECrashReport_CgoFault proves a fault inside a cgo call is captured
// end to end — the real-pipeline counterpart to TestParseCrashDumpCgoFault's
// fixture-based coverage of the same dump shape.
//
// import "C" is not permitted in any _test.go file, regardless of package
// (golang/go#18647, "very difficult and doesn't seem worth it" — Russ Cox);
// the crash victim is therefore a separate module built from source written
// to a temp dir, mirroring the existing subprocess idiom in
// profiler/goroutineleak_test.go rather than this file's own TestMain-based
// role dispatch, which re-execs the test binary itself and so cannot contain
// cgo either.
func TestE2ECrashReport_CgoFault(t *testing.T) {
	if out, err := exec.Command("go", "env", "CGO_ENABLED").CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "1" {
		t.Skip("cgo not available in this build environment")
	}
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(cgoFaultSource), 0o644); err != nil {
		t.Fatalf("writing test source: %s", err)
	}

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("getting repo root: %s", err)
	}

	for _, cmd := range []*exec.Cmd{
		exec.Command("go", "mod", "init", "crashtracker_cgo_test_app"),
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
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%s: %s", build.String(), out)
	}

	received := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertCanonicalAgentRequest(t, r)
		body := decompressGzipBody(t, r)
		select {
		case received <- body:
		default:
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	cmd := exec.Command(binPath)
	cmd.Env = []string{
		"DD_TRACE_AGENT_URL=" + srv.URL,
		"DD_CRASHTRACKING_ENABLED=true",
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting crash victim: %s", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	select {
	case body := <-received:
		report := assertRFC0013Body(t, body)
		errObj := report["error"].(map[string]any)
		// runtime/signal_unix.go and runtime/signal_windows.go report a fault
		// during a cgo call as different top-level crash shapes: a "SIGSEGV: ..."
		// header with a "signal arrived during cgo execution" marker line on
		// Unix, versus an "Exception 0x..." header (classified as
		// error.type=WindowsException; see parse.go's windowsExceptionRe) with
		// a "signal arrived during external code execution" marker line on
		// Windows. Verified directly against both runtime sources rather than
		// assumed identical.
		wantType, wantMarker := "SIGSEGV", "signal arrived during cgo execution"
		if runtime.GOOS == "windows" {
			wantType, wantMarker = "WindowsException", "signal arrived during external code execution"
		}
		if got, _ := errObj["type"].(string); got != wantType {
			t.Errorf("error.type = %q, want %q", got, wantType)
		}
		msg, _ := errObj["message"].(string)
		if !strings.Contains(msg, wantMarker) {
			t.Errorf("error.message = %q, want it to contain the runtime's cgo marker %q", msg, wantMarker)
		}

	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for crash report from monitor")
	}
}

const cgoFaultSource = `package main

/*
void crashtracker_e2e_fault_in_c(void) {
	volatile int *p = 0;
	*p = 1;
}
*/
import "C"

import "github.com/DataDog/dd-trace-go/v2/crashtracker"

func main() {
	if err := crashtracker.Start(); err != nil {
		panic(err)
	}
	C.crashtracker_e2e_fault_in_c()
}
`
