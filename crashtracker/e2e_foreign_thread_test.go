// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker_test

import (
	"encoding/json"
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

// decodeReportBody just decodes: assertRFC0013Body's assumptions (a stack, a
// thread list) don't hold for this report shape, so this test needs its own
// checks rather than that helper.
func decodeReportBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var report map[string]any
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("unmarshal report: %v\nbody: %s", err, body)
	}
	return report
}

// TestE2ECrashReport_ForeignThreadSignal proves the WithForeignThreadSignals
// path end to end against a real fault on a thread created entirely by
// native code — a pthread spawned directly via pthread_create, never
// entering Go — which is exactly the case runtime/debug.SetCrashOutput
// cannot observe at all.
//
// Sending SIGSEGV to the whole process (e.g. via os.Process.Signal) is not
// an equivalent test: the kernel can deliver it to any thread, including a
// Go-tracked one, where it gets converted into an ordinary fatal crash and
// captured by the normal SetCrashOutput path instead — confirmed directly:
// an earlier version of this test sent SIGSEGV process-wide and the report
// that arrived was a real monitor-routed crash dump with a Go stack trace,
// not this mechanism's minimal report. A pure-C pthread is never Go-tracked,
// so that competing interception cannot happen for it by construction.
//
// import "C" is not permitted in any _test.go file regardless of package
// (golang/go#18647); the same subprocess-build pattern as
// TestE2ECrashReport_CgoFault applies here for the same reason.
func TestE2ECrashReport_ForeignThreadSignal(t *testing.T) {
	if out, err := exec.Command("go", "env", "CGO_ENABLED").CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "1" {
		t.Skip("cgo not available in this build environment")
	}
	if runtime.GOOS == "windows" {
		// WithForeignThreadSignals is an intentional no-op on Windows (see
		// foreign_thread_windows.go and doc.go's cgo/foreign-threads section):
		// os/signal has no portable Windows equivalent of SIGSEGV/SIGBUS/SIGILL/
		// SIGFPE to register for. No report can ever arrive here, so the
		// victim process below would just run out its 20-second sleep and the
		// select would time out waiting for something the feature never
		// promises to produce on this platform.
		t.Skip("WithForeignThreadSignals has no effect on windows; see foreign_thread_windows.go")
	}
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(foreignThreadFaultSource), 0o644); err != nil {
		t.Fatalf("writing test source: %s", err)
	}

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("getting repo root: %s", err)
	}

	for _, cmd := range []*exec.Cmd{
		exec.Command("go", "mod", "init", "crashtracker_foreign_thread_test_app"),
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
		t.Fatalf("starting victim: %s", err)
	}
	// Bounds the worst case if the reset-based mitigation somehow does not
	// engage: the process is force-killed regardless, rather than left
	// spinning at high CPU for the rest of the test run.
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	select {
	case body := <-received:
		// Not assertRFC0013Body: that helper assumes every report has a
		// stack and a thread list, which is true for every other crash
		// report this package builds (from a parsed dump) but deliberately
		// not this one — there is genuinely no stack or thread list
		// available for a thread the Go runtime never tracked.
		report := decodeReportBody(t, body)
		if got, _ := report["ddsource"].(string); got != "crashtracker" {
			t.Errorf("ddsource = %q, want %q", got, "crashtracker")
		}
		ddtags, _ := report["ddtags"].(string)
		if ddtags == "" {
			t.Error("ddtags is empty")
		}

		errObj, ok := report["error"].(map[string]any)
		if !ok {
			t.Fatalf("error field missing or not an object; report: %v", report)
		}
		if got, _ := errObj["type"].(string); got != "SIGSEGV" {
			t.Errorf("error.type = %q, want %q", got, "SIGSEGV")
		}
		if errObj["is_crash"] != true {
			t.Errorf("error.is_crash = %v, want true", errObj["is_crash"])
		}
		if errObj["source_type"] != "Crashtracking" {
			t.Errorf("error.source_type = %q, want %q", errObj["source_type"], "Crashtracking")
		}
		msg, _ := errObj["message"].(string)
		if !strings.Contains(msg, "not created by the Go runtime") {
			t.Errorf("error.message = %q, want it to explain the missing stack", msg)
		}
		if _, ok := errObj["stack"]; ok {
			t.Errorf("error.stack = %v, want absent: there is genuinely no stack for this thread", errObj["stack"])
		}
		if _, ok := errObj["threads"]; ok {
			t.Errorf("error.threads = %v, want absent", errObj["threads"])
		}

		sigInfo, ok := report["sig_info"].(map[string]any)
		if !ok {
			t.Fatal("sig_info missing or not an object")
		}
		if sigInfo["si_signo_human_readable"] != "SIGSEGV" {
			t.Errorf("sig_info.si_signo_human_readable = %v, want %q", sigInfo["si_signo_human_readable"], "SIGSEGV")
		}

	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for crash report from monitor")
	}
}

const foreignThreadFaultSource = `package main

/*
#include <pthread.h>

void *crashtracker_e2e_fault_thread(void *arg) {
	volatile int *p = 0;
	*p = 1;
	return 0;
}

// start_foreign_thread creates a pthread directly via the C library, never
// going through Go's runtime thread-creation path at all.
void start_foreign_thread(void) {
	pthread_t t;
	pthread_create(&t, 0, crashtracker_e2e_fault_thread, 0);
}
*/
import "C"

import (
	"time"

	"github.com/DataDog/dd-trace-go/v2/crashtracker"
)

func main() {
	if err := crashtracker.Start(crashtracker.WithForeignThreadSignals(true)); err != nil {
		panic(err)
	}
	C.start_foreign_thread()
	// The foreign thread's fault, its notification, the reset, and the
	// report upload all happen asynchronously; block long enough for that
	// to complete (or for the reset-then-refault to terminate the process)
	// rather than exiting cleanly first and racing it.
	time.Sleep(20 * time.Second)
}
`
