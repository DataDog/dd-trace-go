// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build !windows

package crashtracker_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/DataDog/dd-trace-go/v2/crashtracker"
)

const helperModeEnv = "_CRASHTRACKER_SUPERVISOR_HELPER"

// supervisedFDEnv mirrors the production const in supervisor.go.
const supervisedFDEnv = "_DD_CRASHTRACKING_SUPERVISED_FD"
const strayFDEnv = "_CRASHTRACKER_STRAY_FD"
const probeAPIKey = "crashtracker-probe-api-key"

// helperMarkerFD returns the crash pipe descriptor when the marker names this
// process's parent as its supervisor. Only then is this process the application.
func helperMarkerFD() (fd int, ok bool) {
	pid, value, found := strings.Cut(os.Getenv(supervisedFDEnv), ":")
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
	if (mode == "ignored" || mode == "ignored-reenabled") && !supervisedChild {
		signal.Ignore(syscall.SIGHUP)
	}
	if (mode == "ignored-sigchld" || mode == "ignored-sigchld-panic") && !supervisedChild {
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
		t.Setenv(supervisedFDEnv, "1:"+strconv.Itoa(stray))
	}
	if os.Getenv("GOTRACEBACK") == "crash" {
		if err := unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{}); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "fd-probe" {
		if _, ok := os.LookupEnv(supervisedFDEnv); ok {
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
		// The supervisor resets SIGCHLD to start the child. When the start
		// fails, the application must keep its own setting.
		signal.Ignore(syscall.SIGCHLD)
		if err := crashtracker.Start(); err == nil || !strings.Contains(err.Error(), "start application process") {
			t.Fatalf("Start after removing executable = %v", err)
		}
		if !signal.Ignored(syscall.SIGCHLD) {
			t.Fatal("the failed start did not restore the ignored SIGCHLD")
		}
		fmt.Println("application continued after re-exec failure")
		return
	}
	if mode == "crash-subprocess" {
		probe, err := os.Create(filepath.Join(dir, "subprocess-crash-output"))
		if err != nil {
			t.Fatal(err)
		}
		if err := debug.SetCrashOutput(probe, debug.CrashOptions{}); err != nil {
			t.Fatal(err)
		}
		_ = probe.Close()
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
	if _, ok := os.LookupEnv(supervisedFDEnv); ok {
		t.Fatal("application still has the marker")
	}
	if err := crashtracker.Start(crashtracker.WithEnabled(false)); err != nil {
		t.Fatal(err)
	}
	if expected := os.Getenv("_CRASHTRACKER_ARGV0"); expected != "" && os.Args[0] != expected {
		t.Fatalf("child argv[0] = %q, want %q", os.Args[0], expected)
	}
	if (mode == "ignored" || mode == "ignored-reenabled") && !signal.Ignored(syscall.SIGHUP) {
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
	if mode == "replace-crash-output" {
		f, err := os.Create(filepath.Join(dir, "other-crash-output"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
			t.Fatal(err)
		}
		panic("replaced crash output")
	}
	if mode == "reexec" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSupervisorHelper$")
		cmd.Env = append(os.Environ(), helperModeEnv+"=crash-subprocess")
		var output bytes.Buffer
		cmd.Stderr = &output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		if err := waitForVictim(t, cmd, 5*time.Second); err == nil || cmd.ProcessState.ExitCode() != 2 {
			t.Fatalf("skipped subprocess exit = %v: %s", err, &output)
		}
		if err := os.WriteFile(filepath.Join(dir, "reexec-pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
			t.Fatal(err)
		}
		panic("original application crashed")
	}
	if mode == "crash-subprocess" {
		if err := os.WriteFile(filepath.Join(dir, "subprocess-pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			t.Fatal(err)
		}
		panic("skipped subprocess crashed")
	}
	if mode == "panic" || mode == "held-pipe-panic" || mode == "ignored-sigchld-panic" {
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
	if mode == "close-stdio" {
		// The application signals the end of its output and input, but keeps running.
		_ = os.Stdout.Close()
		_ = os.Stdin.Close()
	}
	ch := make(chan os.Signal, 32)
	if mode == "signals" {
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGUSR1)
		defer signal.Stop(ch)
	}
	if mode == "ignored-reenabled" {
		// For example, a daemon started with nohup that reloads on SIGHUP.
		signal.Notify(ch, syscall.SIGHUP)
		defer signal.Stop(ch)
	}
	if err := os.WriteFile(filepath.Join(dir, "ppid"), []byte(strconv.Itoa(os.Getppid())), 0600); err != nil {
		t.Fatal(err)
	}
	writeReady(t, dir)
	if mode == "held-pipe" || mode == "ignored-sigchld" {
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

// The supervisor must not keep the application's stdin and stdout open. A
// reader of the application's output waits for EOF, and a writer of its input
// waits for a broken pipe, while the application keeps running.
func TestSupervisorReleasesStdio(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSupervisorHelper$")
	detachSupervisor(cmd)
	cmd.Env = append(filterE2EEnv(os.Environ()), helperModeEnv+"=close-stdio",
		"_CRASHTRACKER_HELPER_DIR="+dir, "DD_CRASHTRACKING_ENABLED=true", "GORACE=atexit_sleep_ms=0")
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdinR, stdoutW, nil
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = stdoutW.Close()
	_ = stdinR.Close()
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(dir, "exit"), nil, 0600)
		_ = cmd.Wait()
		_ = stdoutR.Close()
		_ = stdinW.Close()
	})
	app := waitForFile(t, filepath.Join(dir, "ready"))

	gotEOF := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, stdoutR)
		gotEOF <- err
	}()
	select {
	case err := <-gotEOF:
		if err != nil {
			t.Fatalf("reading the application's stdout: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no EOF on stdout after the application closed it: the supervisor keeps a copy")
	}

	signal.Ignore(syscall.SIGPIPE)
	t.Cleanup(func() { signal.Reset(syscall.SIGPIPE) })
	if _, err := stdinW.Write([]byte("x")); !errors.Is(err, syscall.EPIPE) {
		t.Fatalf("write to the application's closed stdin = %v, want EPIPE: the supervisor keeps a copy", err)
	}
	pid, err := strconv.Atoi(string(app))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("the application %d is no longer running: %v", pid, err)
	}
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

func TestSupervisorWarnsWhenCrashOutputIsMissing(t *testing.T) {
	const warning = "crashtracker: the application crashed with empty crash output: another component may have called debug.SetCrashOutput, or the crash occurred outside the Go runtime"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	for _, tt := range []struct {
		name      string
		mode      string
		code      string
		traceback string
		signal    syscall.Signal
		wantExit  int
		wantCount int
	}{
		{name: "replaced pipe", mode: "replace-crash-output", wantExit: 2, wantCount: 1},
		{name: "replaced pipe with SIGABRT", mode: "replace-crash-output", traceback: "crash", wantExit: 128 + int(syscall.SIGABRT), wantCount: 1},
		{name: "SIGTERM", mode: "wait", signal: syscall.SIGTERM, wantExit: -1},
		{name: "exit 2", mode: "exit", code: "2", wantExit: 2},
		{name: "normal exit", mode: "exit", code: "0", wantExit: 0},
		{name: "working crash pipe", mode: "panic", wantExit: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			traceback := tt.traceback
			if traceback == "" {
				traceback = "single"
			}
			cmd, dir := startSupervisorHelper(t, tt.mode, "_CRASHTRACKER_EXIT_CODE="+tt.code, "DD_TRACE_AGENT_URL="+srv.URL, "GOTRACEBACK="+traceback)
			if tt.signal != 0 {
				waitForFile(t, filepath.Join(dir, "ready"))
				if err := cmd.Process.Signal(tt.signal); err != nil {
					t.Fatal(err)
				}
			}
			_ = waitForVictim(t, cmd, 15*time.Second)
			if got := cmd.ProcessState.ExitCode(); got != tt.wantExit {
				t.Fatalf("exit code = %d, want %d", got, tt.wantExit)
			}
			if tt.signal != 0 {
				status := cmd.ProcessState.Sys().(syscall.WaitStatus)
				if !status.Signaled() || status.Signal() != tt.signal {
					t.Fatalf("supervisor status = %v, want signal %v", status, tt.signal)
				}
			}
			output, err := os.ReadFile(filepath.Join(dir, "stderr"))
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(string(output), warning); got != tt.wantCount {
				t.Fatalf("warning count = %d, want %d: %s", got, tt.wantCount, output)
			}
		})
	}
}

func TestSupervisorSkipsApplicationSubprocess(t *testing.T) {
	received := make(chan []byte, 2)
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
	cmd, dir := startSupervisorHelper(t, "reexec", "DD_TRACE_AGENT_URL="+srv.URL)
	_ = waitForVictim(t, cmd, 5*time.Second)
	if got := cmd.ProcessState.ExitCode(); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
	subprocessPID := waitForFile(t, filepath.Join(dir, "reexec-pid"))
	pid := waitForFile(t, filepath.Join(dir, "subprocess-pid"))
	if string(pid) != string(subprocessPID) {
		t.Fatalf("Start returned in PID %s, want the subprocess PID %s", pid, subprocessPID)
	}
	// The probe must retain crash output after the skipped Start call.
	if output, err := os.ReadFile(filepath.Join(dir, "subprocess-crash-output")); err != nil || !bytes.Contains(output, []byte("panic: skipped subprocess crashed")) {
		t.Fatalf("skipped subprocess crash output = %q, error = %v", output, err)
	}
	select {
	case body := <-received:
		assertCrashReport(t, body, "panic", "original application crashed")
	default:
		t.Error("no report for the original application crash")
	}
	if len(received) != 0 {
		t.Error("the skipped subprocess uploaded a crash report")
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

// A signal that was ignored at start stays ignored in the application, but the
// application can enable it again. The supervisor must then forward it.
func TestSupervisorForwardsReenabledSignals(t *testing.T) {
	cmd, dir := startSupervisorHelper(t, "ignored-reenabled")
	waitForFile(t, filepath.Join(dir, "ready"))
	if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, _ := os.ReadFile(filepath.Join(dir, "signals"))
		if string(data) == strconv.Itoa(int(syscall.SIGHUP)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the application did not receive SIGHUP that it enabled again (signals = %q)", data)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(dir, "exit"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	_ = waitForVictim(t, cmd, 5*time.Second)
	if cmd.ProcessState.ExitCode() != 3 {
		t.Fatalf("exit status = %v, want code 3", cmd.ProcessState)
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

// An application that ignores SIGCHLD before Start must not make the kernel
// auto-reap its child. The supervisor resets SIGCHLD, so Wait sees the status.
func TestSupervisorKeepsStatusWithIgnoredSIGCHLD(t *testing.T) {
	cmd, dir := startSupervisorHelper(t, "ignored-sigchld")
	_ = waitForVictim(t, cmd, 5*time.Second)
	output, _ := os.ReadFile(filepath.Join(dir, "stderr"))
	if got := cmd.ProcessState.ExitCode(); got != 3 {
		t.Fatalf("supervisor exit code = %d, want the child's 3: %s", got, output)
	}
	if strings.Contains(string(output), "wait for application process failed") {
		t.Fatalf("supervisor failed to wait for the child: %s", output)
	}
}

func TestSupervisorUploadsWithIgnoredSIGCHLD(t *testing.T) {
	received := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received <- struct{}{}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	cmd, dir := startSupervisorHelper(t, "ignored-sigchld-panic", "DD_TRACE_AGENT_URL="+srv.URL)
	_ = waitForVictim(t, cmd, 10*time.Second)
	output, _ := os.ReadFile(filepath.Join(dir, "stderr"))
	if got := cmd.ProcessState.ExitCode(); got != 2 {
		t.Fatalf("supervisor exit code = %d, want the child's panic status 2: %s", got, output)
	}
	select {
	case <-received:
	default:
		t.Fatalf("the crash report was not uploaded: %s", output)
	}
}

// The upload ignores stop signals: the child is gone and the report is what
// the supervisor is for.
func TestSupervisorFinishesUploadAfterStopSignal(t *testing.T) {
	inFlight := make(chan struct{})
	signaled := make(chan struct{})
	received := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(inFlight)
		select {
		case <-signaled:
		case <-time.After(10 * time.Second):
		}
		// Give the signal time to reach the supervisor before the response.
		time.Sleep(200 * time.Millisecond)
		received <- struct{}{}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	cmd, dir := startSupervisorHelper(t, "panic", "DD_TRACE_AGENT_URL="+srv.URL)
	select {
	case <-inFlight:
	case <-time.After(10 * time.Second):
		t.Fatal("the upload did not start")
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	close(signaled)
	_ = waitForVictim(t, cmd, 10*time.Second)
	output, _ := os.ReadFile(filepath.Join(dir, "stderr"))
	if got := cmd.ProcessState.ExitCode(); got != 2 {
		t.Fatalf("supervisor exit code = %d, want the child's panic status 2: %s", got, output)
	}
	select {
	case <-received:
	default:
		t.Fatalf("the upload did not complete: %s", output)
	}
	if !strings.Contains(string(output), "crashtracker: upload succeeded") {
		t.Fatalf("supervisor did not log upload success: %s", output)
	}
}
