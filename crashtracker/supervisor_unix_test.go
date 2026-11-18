// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build !windows

package crashtracker_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/DataDog/dd-trace-go/v2/crashtracker"
)

const helperModeEnv = "_CRASHTRACKER_SUPERVISOR_HELPER"
const inheritedFDEnv = "_DD_CRASHTRACKING_SUPERVISED_FD"
const strayFDEnv = "_CRASHTRACKER_STRAY_FD"
const probeAPIKey = "crashtracker-probe-api-key"

// helperMarkerFD returns the crash pipe descriptor when the marker names this
// process's parent as its supervisor. Only then is this process the application.
func helperMarkerFD() (fd int, ok bool) {
	pid, value, found := strings.Cut(os.Getenv(inheritedFDEnv), ":")
	if !found || pid != strconv.Itoa(os.Getppid()) {
		return 0, false
	}
	fd, err := strconv.Atoi(value)
	return fd, err == nil
}

func TestSupervisorHelper(t *testing.T) {
	mode := os.Getenv(helperModeEnv)
	if mode == "" {
		return
	}
	dir := os.Getenv("_CRASHTRACKER_HELPER_DIR")
	markerFD, supervisedChild := helperMarkerFD()
	if mode == "hold-pipe" {
		if err := os.WriteFile(filepath.Join(dir, "holder"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			t.Fatal(err)
		}
		for {
			time.Sleep(time.Hour)
		}
	}
	if mode == "ignored" && !supervisedChild {
		signal.Ignore(syscall.SIGHUP)
	}
	if (mode == "wait-error" || mode == "wait-error-panic") && !supervisedChild {
		signal.Ignore(syscall.SIGCHLD)
	}
	if mode == "stray-marker" && !supervisedChild && os.Getenv(strayFDEnv) == "" {
		// A descriptor that a stray marker names must survive Start in the application.
		f, err := os.CreateTemp(dir, "stray")
		if err != nil {
			t.Fatal(err)
		}
		stray, err := unix.FcntlInt(f.Fd(), unix.F_DUPFD, 10)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv(strayFDEnv, strconv.Itoa(stray))
		// PID 1 is never the parent of this helper.
		t.Setenv(inheritedFDEnv, "1:"+strconv.Itoa(stray))
	}
	if os.Getenv("GOTRACEBACK") == "crash" {
		if err := unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{}); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "fd-probe" {
		if _, ok := os.LookupEnv(inheritedFDEnv); ok {
			t.Fatal("subprocess inherited the marker")
		}
		for fd := 3; fd < 256; fd++ {
			var stat unix.Stat_t
			if unix.Fstat(fd, &stat) == nil && fmt.Sprint(stat.Dev, ":", stat.Ino) == os.Getenv("_CRASHTRACKER_PIPE_ID") {
				t.Fatalf("subprocess inherited crash pipe at fd %d", fd)
			}
		}
		return
	}
	if mode == "reexec-failure" {
		if err := os.Remove(os.Args[0]); err != nil {
			t.Fatal(err)
		}
		if err := crashtracker.Start(); err == nil || !strings.Contains(err.Error(), "start application process") {
			t.Fatalf("Start after removing executable = %v", err)
		}
		fmt.Println("application continued after re-exec failure")
		return
	}
	var pipeID string
	if supervisedChild {
		fd := markerFD
		var stat unix.Stat_t
		if err := unix.Fstat(fd, &stat); err != nil {
			t.Fatal(err)
		}
		pipeID = fmt.Sprint(stat.Dev, ":", stat.Ino)
		if mode == "held-pipe" || mode == "held-pipe-panic" {
			dup, err := unix.Dup(fd)
			if err != nil {
				t.Fatal(err)
			}
			pipe := os.NewFile(uintptr(dup), "held-crash-pipe")
			held := exec.Command(os.Args[0], "-test.run=^TestSupervisorHelper$")
			held.Env = append(os.Environ(), helperModeEnv+"=hold-pipe")
			held.ExtraFiles = []*os.File{pipe}
			if err := held.Start(); err != nil {
				t.Fatal(err)
			}
			_ = pipe.Close()
			_ = held.Process.Release()
			waitForFile(t, filepath.Join(dir, "holder"))
		}
	}
	// The child's options must not disable its inherited crash output.
	var opts []crashtracker.Option
	if pipeID != "" {
		opts = append(opts, crashtracker.WithEnabled(false))
	}
	if !supervisedChild {
		switch mode {
		case "env-probe":
			opts = append(opts, crashtracker.WithAPIKey(probeAPIKey))
		case "enable-override":
			opts = append(opts, crashtracker.WithEnabled(true))
		}
	}
	if err := crashtracker.Start(opts...); err != nil {
		t.Fatal(err)
	}
	if _, ok := os.LookupEnv(inheritedFDEnv); ok {
		t.Fatal("application still has the marker")
	}
	if err := crashtracker.Start(crashtracker.WithEnabled(false)); err != nil {
		t.Fatal(err)
	}
	if expected := os.Getenv("_CRASHTRACKER_ARGV0"); expected != "" && os.Args[0] != expected {
		t.Fatalf("child argv[0] = %q, want %q", os.Args[0], expected)
	}
	if mode == "ignored" && !signal.Ignored(syscall.SIGHUP) {
		t.Fatal("child did not inherit ignored SIGHUP")
	}
	if mode == "fd" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSupervisorHelper$")
		cmd.Env = append(os.Environ(), helperModeEnv+"=fd-probe", "_CRASHTRACKER_PIPE_ID="+pipeID)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("probe: %v: %s", err, output)
		}
		return
	}
	if mode == "panic" || mode == "held-pipe-panic" || mode == "wait-error-panic" {
		panic("child options ignored")
	}
	if fd := os.Getenv(strayFDEnv); fd != "" {
		n, err := strconv.Atoi(fd)
		if err != nil {
			t.Fatal(err)
		}
		var stat unix.Stat_t
		if err := unix.Fstat(n, &stat); err != nil {
			t.Fatalf("Start closed the descriptor named by a stray marker: %v", err)
		}
	}
	if mode == "env-probe" {
		result := "clean"
		for _, entry := range os.Environ() {
			if strings.Contains(entry, probeAPIKey) {
				result = "leaked into the application environment"
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "env-probe"), []byte(result), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "stdin" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "stdin"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ch := make(chan os.Signal, 32)
	if mode == "signals" {
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGUSR1)
		defer signal.Stop(ch)
	}
	if err := os.WriteFile(filepath.Join(dir, "ppid"), []byte(strconv.Itoa(os.Getppid())), 0600); err != nil {
		t.Fatal(err)
	}
	writeReady(t, dir)
	if mode == "held-pipe" || mode == "wait-error" {
		os.Exit(3)
	}
	switch mode {
	case "exit", "disabled", "env-probe", "stdin", "enable-override", "stray-marker":
		code, _ := strconv.Atoi(os.Getenv("_CRASHTRACKER_EXIT_CODE"))
		os.Exit(code)
	}
	var observed []string
	for {
		select {
		case sig := <-ch:
			observed = append(observed, strconv.Itoa(int(sig.(syscall.Signal))))
			if err := os.WriteFile(filepath.Join(dir, "signals"), []byte(strings.Join(observed, "\n")), 0600); err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Millisecond):
			if _, err := os.Stat(filepath.Join(dir, "exit")); err == nil {
				os.Exit(3)
			}
		}
	}
}

func detachSupervisor(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func startSupervisorHelper(t *testing.T, mode string, env ...string) (*exec.Cmd, string) {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSupervisorHelper$")
	detachSupervisor(cmd)
	cmd.Env = append(filterE2EEnv(os.Environ()), helperModeEnv+"="+mode,
		"_CRASHTRACKER_HELPER_DIR="+dir, "DD_CRASHTRACKING_ENABLED=true", "GORACE=atexit_sleep_ms=0")
	cmd.Env = append(cmd.Env, env...)
	if mode == "exit" {
		cmd.Args[0] = "crashtracker-original-argv0"
		cmd.Env = append(cmd.Env, "_CRASHTRACKER_ARGV0="+cmd.Args[0])
	}
	if mode == "stdin" {
		cmd.Stdin = strings.NewReader("stdin-pass-through\n")
	}
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = null.Close() })
	stderr, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stderr.Close() })
	cmd.Stdout, cmd.Stderr = null, stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		if data, err := os.ReadFile(filepath.Join(dir, "ready")); err == nil {
			if pid, err := strconv.Atoi(string(data)); err == nil && pid != cmd.Process.Pid {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
		if data, err := os.ReadFile(filepath.Join(dir, "holder")); err == nil {
			if pid, err := strconv.Atoi(string(data)); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	return cmd, dir
}

func TestSupervisorExitCodes(t *testing.T) {
	for _, code := range []int{0, 3} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			posted := make(chan struct{}, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				posted <- struct{}{}
				w.WriteHeader(http.StatusAccepted)
			}))
			defer srv.Close()
			cmd, dir := startSupervisorHelper(t, "exit", "_CRASHTRACKER_EXIT_CODE="+strconv.Itoa(code), "DD_TRACE_AGENT_URL="+srv.URL)
			_ = waitForVictim(t, cmd, 5*time.Second)
			if got := cmd.ProcessState.ExitCode(); got != code {
				t.Fatalf("exit code = %d, want %d", got, code)
			}
			pid, _ := strconv.Atoi(string(waitForFile(t, filepath.Join(dir, "ready"))))
			if pid == cmd.Process.Pid {
				t.Fatal("application did not run as a child")
			}
			select {
			case <-posted:
				t.Fatal("upload on normal exit")
			default:
			}
		})
	}
}

func TestSupervisorSignals(t *testing.T) {
	cmd, dir := startSupervisorHelper(t, "signals")
	waitForFile(t, filepath.Join(dir, "ready"))
	signals := []syscall.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGUSR1}
	for i, sig := range signals {
		if err := cmd.Process.Signal(sig); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for {
			data, _ := os.ReadFile(filepath.Join(dir, "signals"))
			if len(strings.Fields(string(data))) >= i+1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("child did not observe %v", sig)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	time.Sleep(150 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "exit"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	_ = waitForVictim(t, cmd, 5*time.Second)
	if cmd.ProcessState.ExitCode() != 3 {
		t.Fatalf("exit status = %v, want code 3", cmd.ProcessState)
	}
	data, err := os.ReadFile(filepath.Join(dir, "signals"))
	if err != nil {
		t.Fatal(err)
	}
	expected := make([]string, 0, len(signals))
	for _, sig := range signals {
		expected = append(expected, strconv.Itoa(int(sig)))
	}
	if got, want := string(data), strings.Join(expected, "\n"); got != want {
		t.Fatalf("signals = %q, want exactly %q", got, want)
	}
}

func TestSupervisorChildFatalSignalIsReraised(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL} {
		t.Run(sig.String(), func(t *testing.T) {
			cmd, dir := startSupervisorHelper(t, "wait")
			pid, err := strconv.Atoi(string(waitForFile(t, filepath.Join(dir, "ready"))))
			if err != nil {
				t.Fatal(err)
			}
			if err := syscall.Kill(pid, sig); err != nil {
				t.Fatal(err)
			}
			_ = waitForVictim(t, cmd, 5*time.Second)
			status := cmd.ProcessState.Sys().(syscall.WaitStatus)
			if !status.Signaled() || status.Signal() != sig {
				t.Fatalf("supervisor status = %v, want %v", status, sig)
			}
		})
	}
}

func TestSupervisorSubprocessDoesNotInheritPipe(t *testing.T) {
	cmd, _ := startSupervisorHelper(t, "fd")
	if err := waitForVictim(t, cmd, 5*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorDisabled(t *testing.T) {
	cmd, dir := startSupervisorHelper(t, "disabled", "DD_CRASHTRACKING_ENABLED=false")
	if err := waitForVictim(t, cmd, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := string(waitForFile(t, filepath.Join(dir, "ready"))); got != strconv.Itoa(cmd.Process.Pid) {
		t.Fatalf("disabled Start created a child: pid=%s, original=%d", got, cmd.Process.Pid)
	}
}

func TestSupervisorReexecFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper")
	src, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("copy executable: %v, close: %v", copyErr, closeErr)
	}
	cmd := exec.Command(path, "-test.run=^TestSupervisorHelper$")
	detachSupervisor(cmd)
	cmd.Env = append(filterE2EEnv(os.Environ()), helperModeEnv+"=reexec-failure", "DD_CRASHTRACKING_ENABLED=true", "GORACE=atexit_sleep_ms=0")
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "application continued after re-exec failure") {
		t.Fatalf("fail-open helper: %v: %s", err, output)
	}
}

func TestSupervisorChildIgnoresOptions(t *testing.T) {
	received := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received <- struct{}{}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	cmd, _ := startSupervisorHelper(t, "panic", "DD_TRACE_AGENT_URL="+srv.URL)
	_ = waitForVictim(t, cmd, 5*time.Second)
	select {
	case <-received:
	default:
		t.Fatal("child options disabled the inherited crash pipe")
	}
}

func TestSupervisorPanicCrashDoesNotDumpSupervisor(t *testing.T) {
	received := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received <- struct{}{}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	cmd, dir := startSupervisorHelper(t, "panic", "GOTRACEBACK=crash", "DD_TRACE_AGENT_URL="+srv.URL)
	_ = waitForVictim(t, cmd, 15*time.Second)
	status := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if cmd.ProcessState.ExitCode() != 134 || status.Signaled() {
		t.Fatalf("supervisor status = %v, want exit code 134 without a signal", status)
	}
	select {
	case <-received:
	default:
		t.Fatal("crash report did not arrive")
	}
	output, err := os.ReadFile(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	_, afterUpload, ok := strings.Cut(string(output), "crashtracker: upload succeeded")
	if !ok {
		t.Fatal("supervisor did not log upload success")
	}
	if strings.Contains(afterUpload, "SIGABRT: abort") {
		t.Fatal("supervisor emitted a second SIGABRT dump after upload")
	}
}

func TestSupervisorBoundsCrashPipeEOFWait(t *testing.T) {
	started := time.Now()
	cmd, dir := startSupervisorHelper(t, "held-pipe")
	_ = waitForVictim(t, cmd, 5*time.Second)
	if cmd.ProcessState.ExitCode() != 3 {
		t.Fatalf("supervisor exit code = %d, want 3", cmd.ProcessState.ExitCode())
	}
	if time.Since(started) < 2*time.Second {
		t.Fatal("supervisor did not wait for the held pipe")
	}
	output, err := os.ReadFile(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(output), "crashtracker: timed out waiting for crash pipe EOF") != 1 {
		t.Fatalf("missing single EOF warning: %s", output)
	}
}

func TestSupervisorPreservesIgnoredSignals(t *testing.T) {
	cmd, dir := startSupervisorHelper(t, "ignored")
	waitForFile(t, filepath.Join(dir, "ready"))
	if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "exit"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	_ = waitForVictim(t, cmd, 5*time.Second)
	if cmd.ProcessState.ExitCode() != 3 {
		output, _ := os.ReadFile(filepath.Join(dir, "stderr"))
		t.Fatalf("ignored SIGHUP did not preserve normal exit: %v: %s", cmd.ProcessState, output)
	}
}

func TestSupervisorUploadsDumpWhenCrashPipeStaysOpen(t *testing.T) {
	received := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received <- struct{}{}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	started := time.Now()
	cmd, dir := startSupervisorHelper(t, "held-pipe-panic", "DD_TRACE_AGENT_URL="+srv.URL)
	_ = waitForVictim(t, cmd, 15*time.Second)
	if got := cmd.ProcessState.ExitCode(); got != 2 {
		t.Fatalf("supervisor exit code = %d, want the panic exit code 2", got)
	}
	if time.Since(started) < 2*time.Second {
		t.Fatal("supervisor did not wait for the held pipe")
	}
	select {
	case <-received:
	default:
		output, _ := os.ReadFile(filepath.Join(dir, "stderr"))
		t.Fatalf("held crash pipe lost the report: %s", output)
	}
}

func TestSupervisorIgnoresStrayMarker(t *testing.T) {
	cmd, dir := startSupervisorHelper(t, "stray-marker")
	_ = waitForVictim(t, cmd, 5*time.Second)
	if got := cmd.ProcessState.ExitCode(); got != 0 {
		output, _ := os.ReadFile(filepath.Join(dir, "stderr"))
		t.Fatalf("supervisor exit code = %d: %s", got, output)
	}
	pid, _ := strconv.Atoi(string(waitForFile(t, filepath.Join(dir, "ready"))))
	if pid == cmd.Process.Pid {
		t.Fatal("a stray marker made the process skip supervision")
	}
}

func TestSupervisorKeepsAPIKeyOutOfChildEnvironment(t *testing.T) {
	cmd, dir := startSupervisorHelper(t, "env-probe")
	_ = waitForVictim(t, cmd, 5*time.Second)
	if got := string(waitForFile(t, filepath.Join(dir, "env-probe"))); got != "clean" {
		t.Fatalf("WithAPIKey %s", got)
	}
}

func TestSupervisorPassesStdinToChild(t *testing.T) {
	cmd, dir := startSupervisorHelper(t, "stdin")
	_ = waitForVictim(t, cmd, 5*time.Second)
	if got, want := string(waitForFile(t, filepath.Join(dir, "stdin"))), "stdin-pass-through\n"; got != want {
		t.Fatalf("child stdin = %q, want %q", got, want)
	}
}

func TestSupervisorIsParentOfChild(t *testing.T) {
	cmd, dir := startSupervisorHelper(t, "exit")
	_ = waitForVictim(t, cmd, 5*time.Second)
	if got, want := string(waitForFile(t, filepath.Join(dir, "ppid"))), strconv.Itoa(cmd.Process.Pid); got != want {
		t.Fatalf("child os.Getppid() = %s, want the supervisor PID %s", got, want)
	}
}

func TestSupervisorEnabledOptionOverridesEnvironment(t *testing.T) {
	cmd, dir := startSupervisorHelper(t, "enable-override", "DD_CRASHTRACKING_ENABLED=false")
	_ = waitForVictim(t, cmd, 5*time.Second)
	pid, _ := strconv.Atoi(string(waitForFile(t, filepath.Join(dir, "ready"))))
	if pid == cmd.Process.Pid {
		t.Fatal("WithEnabled(true) did not override DD_CRASHTRACKING_ENABLED=false")
	}
}
