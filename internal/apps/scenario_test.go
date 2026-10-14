// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package apps

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

// TestApps defines the scenarios that are run against the test apps. See
// ./README.md for more details.
func TestScenario(t *testing.T) {
	wc := newWorkloadConfig(t)
	t.Run("memory-leak", func(t *testing.T) {
		scenarios := []struct {
			name      string
			endpoints []string
		}{
			{"goroutine", []string{"/lorem", "/ipsum"}},
			{"heap", []string{"/lorem", "/dolor"}},
			{"goroutine-heap", []string{"/lorem", "/sit"}},
		}

		for _, s := range scenarios {
			t.Run(s.name, func(t *testing.T) {
				lc := newLaunchConfig(t)
				process := lc.Launch(t)
				defer process.Stop(t)
				wc.HitEndpoints(t, process, s.endpoints...)
			})
		}
	})

	t.Run("unit-of-work", func(t *testing.T) {
		scenarios := []struct {
			version   string
			endpoints []string
		}{
			{"v1", []string{"/foo", "/bar"}},
			{"v2", []string{"/foo", "/bar", "/bar"}},
		}
		for _, s := range scenarios {
			t.Run(s.version, func(t *testing.T) {
				lc := newLaunchConfig(t)
				lc.Version = s.version
				process := lc.Launch(t)
				defer process.Stop(t)
				wc.HitEndpoints(t, process, s.endpoints...)
			})
		}
	})

	t.Run("gc-overhead", func(t *testing.T) {
		scenarios := []struct {
			version   string
			endpoints []string
		}{
			{"v1", []string{"/vehicles/update_location", "/vehicles/list"}},
		}
		for _, s := range scenarios {
			t.Run(s.version, func(t *testing.T) {
				lc := newLaunchConfig(t)
				lc.Version = s.version
				process := lc.Launch(t)
				defer process.Stop(t)
				wc.HitEndpoints(t, process, s.endpoints...)
			})
		}
	})

	t.Run("worker-pool-bottleneck", func(t *testing.T) {
		scenarios := []struct {
			version   string
			endpoints []string
		}{
			{"v1", []string{"/queue/push"}},
		}
		for _, s := range scenarios {
			t.Run(s.version, func(t *testing.T) {
				lc := newLaunchConfig(t)
				lc.Version = s.version
				process := lc.Launch(t)
				defer process.Stop(t)
				wc.HitEndpoints(t, process, s.endpoints...)
			})
		}
	})

	// telemetry-errors is a manual verification harness for dogfooding
	// internal/telemetry/log's ReportError/ReportPanic API (PR #4997) — not
	// wired into .github/workflows/test-apps.cue, so it never runs in CI.
	// Run it by hand: DD_SITE=datad0g.com DD_API_KEY=... docker-compose run
	// --build scenario 'telemetry-errors/v1$', then search Error Tracking for
	// service:dd-trace-go/telemetry-errors.
	t.Run("telemetry-errors", func(t *testing.T) {
		scenarios := []struct {
			version   string
			endpoints []string
		}{
			{"v1", []string{"/decision-maker"}},
		}
		for _, s := range scenarios {
			t.Run(s.version, func(t *testing.T) {
				lc := newLaunchConfig(t)
				lc.Version = s.version
				process := lc.Launch(t)
				defer process.Stop(t)
				wc.HitEndpoints(t, process, s.endpoints...)
			})
		}
	})

	// crashtracker deliberately crashes the app on /crash rather than
	// serving a load pattern, so it cannot reuse wc.HitEndpoints (which
	// asserts every request succeeds) or process.Stop (which asserts a
	// clean SIGINT exit): there is nothing left to gracefully stop once the
	// process has already crashed.
	t.Run("crashtracker", func(t *testing.T) {
		t.Run("panic", func(t *testing.T) {
			lc := newLaunchConfig(t)
			process := lc.Launch(t)

			// Bounded so a connection that never completes (the server side of
			// the crash race, or a network-level hang) can't itself stall the
			// nightly job; the crash is still triggered even if this request
			// times out; see the comment below.
			client := http.Client{Timeout: 10 * time.Second}
			// The handler panics in a goroutine after writing its own
			// response, so this request races the crash: it can complete
			// normally or fail with a connection reset depending on which
			// side of that race wins. Both outcomes mean the crash was
			// triggered, so only the process's own exit status below is
			// asserted on, not this request's result.
			resp, err := client.Get("http://" + process.HostPort + "/crash")
			if err == nil {
				resp.Body.Close()
			}

			// Worst case for the monitor's own upload attempt is 3 retries at a
			// 10s client timeout each plus backoff (see crashtracker/upload.go's
			// uploadAttempts/uploadRetryBackoff), ~31.5s; 45s leaves margin
			// without leaving this open-ended if something regresses into an
			// actual hang -- the previous unbounded receive could otherwise
			// block until the surrounding go test's own timeout.
			const exitWait = 45 * time.Second
			select {
			case err := <-process.wait:
				require.Error(t, err, "expected the app to exit non-zero after crashing")
			case <-time.After(exitWait):
				process.proc.Process.Kill()
				t.Fatalf("app did not exit within %s of the crash request; output so far:\n%s", exitWait, process.Tail())
			}

			// process.wait only fires once every holder of the app's stderr fd
			// has closed it -- including the detached crashtracker monitor
			// (see crashtracker/monitor.go's spawnMonitor), which inherits that
			// fd and keeps it open until its own upload attempt finishes. So by
			// the time we get here, the monitor has already logged its outcome
			// into process.Tail(); assert on that directly rather than just on
			// the app having crashed, which the deliberate panic guarantees
			// regardless of whether the monitor ever started or its upload
			// succeeded.
			tail := process.Tail()
			require.Contains(t, tail, "crashtracker: upload succeeded",
				"crash report was not confirmed delivered; captured output:\n%s", tail)
		})
	})
}

func newWorkloadConfig(t *testing.T) (wc workloadConfig) {
	parseEnv(t, "DD_TEST_APPS_REQUESTS_PER_SECOND", &wc.RPS, 5)
	parseEnv(t, "DD_TEST_APPS_TOTAL_DURATION", &wc.TotalDuration, 60*time.Second)
	return
}

func (wc *workloadConfig) HitEndpoints(t *testing.T, p process, endpoints ...string) {
	t.Logf("Hitting endpoints with %d req/sec: %v", wc.RPS, endpoints)

	ctx, cancel := context.WithTimeout(context.Background(), wc.TotalDuration)
	defer cancel()

	ticker := time.NewTicker(time.Second / time.Duration(wc.RPS))
	defer ticker.Stop()

	var eg errgroup.Group
loop:
	for {
		select {
		case <-ticker.C:
			for _, endpoint := range endpoints {
				url := "http://" + p.HostPort + endpoint
				eg.Go(func() error {
					req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
					if err != nil {
						return err
					}
					res, err := http.DefaultClient.Do(req)
					if err != nil {
						return err
					}
					return res.Body.Close()
				})
			}
		case <-ctx.Done():
			break loop
		}
	}
	if err := eg.Wait(); !errors.Is(err, context.DeadlineExceeded) {
		require.NoError(t, err)
	}
}

// workloadConfig holds workload configuration parameters that are used to
// generate load against the test apps.
type workloadConfig struct {
	TotalDuration time.Duration
	RPS           int
}

func newLaunchConfig(t *testing.T) (lc launchConfig) {
	lc.App = appName(t)
	lc.Service = serviceName(t)
	lc.Version = "v1"
	parseEnv(t, "DD_ENV", &lc.Env, "dev")
	parseEnv(t, "DD_TEST_APPS_PROFILE_PERIOD", &lc.ProfilePeriod, 10*time.Second)
	lc.Tags = strings.TrimSpace(fmt.Sprintf("%s run_id:%d", os.Getenv("DD_TAGS"), rand.Uint64()))
	return
}

type launchConfig struct {
	// App is the name of the test app. It must be the same as the name of the
	// folder containing the main.go file.
	App string
	// Args is a list of additional command line arguments passed to the test
	// app.
	Args []string
	// Service is passed as DD_SERVICE to the test app.
	Service string
	// Version is passed as DD_VERSION to the test app.
	Version string
	// Env is passed as DD_ENV to the test app.
	Env string
	// Tags is passed as DD_TAGS to the test app.
	Tags string
	// ProfilePeriod is passed to the test app via a flag.
	ProfilePeriod time.Duration
}

func appName(t *testing.T) string {
	return strings.Split(t.Name(), "/")[1]
}

func serviceName(t *testing.T) string {
	// Allow overriding the service name via env var
	ddService := os.Getenv("DD_SERVICE")
	if ddService != "" {
		return ddService
	}

	// Otherwise derive the service name from the test name
	return "dd-trace-go/" + strings.Join(strings.Split(t.Name(), "/")[1:], "/")
}

func (a *launchConfig) Launch(t *testing.T) (p process) {
	// Start app
	if p.HostPort == "" {
		p.HostPort = "localhost:8080"
	}

	binPath := filepath.Join(os.TempDir(), a.App)
	defer os.Remove(binPath)

	// Launch test app as its own binary. This produces a more realistic looking
	// profile than running the workload from a TestXXX func.
	cmd := fmt.Sprintf(
		"go build -o %[1]s ./%[2]s && exec %[1]s -http %[3]s -period %[4]s %[5]s",
		binPath,
		a.App,
		p.HostPort,
		a.ProfilePeriod,
		strings.Join(a.Args, " "),
	)
	proc := exec.Command("bash", "-c", cmd)
	env := []string{
		"DD_TAGS=" + a.Tags,
		"DD_SERVICE=" + a.Service,
		"DD_VERSION=" + a.Version,
		"DD_ENV=" + a.Env,
	}
	proc.Env = append(os.Environ(), env...)
	r, w := io.Pipe()
	proc.Stdout = io.MultiWriter(w, os.Stdout)
	proc.Stderr = io.MultiWriter(w, os.Stderr)

	t.Logf(
		"Launching %s with env: %s",
		a.App,
		strings.Join(env, " "),
	)
	require.NoError(t, proc.Start())

	p.wait = make(chan error, 1)
	go func() {
		err := proc.Wait()
		// Unblock scanner.Scan if app crashes before listening
		w.CloseWithError(err)
		p.wait <- err
	}()

	// Wait until app is ready
	var listening bool
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "Listening on:") {
			listening = true
			break
		}
	}
	// Keep draining r to avoid blocking the app; also capture a bounded
	// tail so scenarios that crash the app on purpose (see the crashtracker
	// subtest) can inspect what it logged after this point.
	p.tail = &tailBuffer{}
	go io.Copy(io.MultiWriter(io.Discard, p.tail), r)

	// Check startup succeeded
	require.True(t, listening, "app failed to start")

	p.proc = proc
	return
}

type process struct {
	HostPort string
	wait     chan error
	proc     *exec.Cmd
	tail     *tailBuffer
}

func (ti *process) Stop(t *testing.T) {
	// Shutdown app
	ti.proc.Process.Signal(os.Interrupt)
	require.NoError(t, <-ti.wait)
}

// Tail returns the app's output captured since Launch finished waiting for
// startup. Safe to call while the app is still running.
func (ti *process) Tail() string {
	return ti.tail.String()
}

// tailBufferCap bounds tailBuffer's memory use; scenarios only need enough to
// find a short marker line, not the app's full output.
const tailBufferCap = 64 * 1024

// tailBuffer is an io.Writer that keeps up to tailBufferCap bytes and
// silently drops the rest, reporting the full input length as written either
// way so it never turns into a write error for callers (in particular
// io.MultiWriter, which would otherwise abort every other writer sharing the
// copy) once the cap is reached.
type tailBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	if remaining := tailBufferCap - b.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.buf.Write(p)
	}
	return n, nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func parseEnv[T any](t *testing.T, name string, dst *T, fallback T) {
	s := os.Getenv(name)
	if s == "" {
		t.Logf("%s: %v (default)", name, fallback)
		*dst = any(fallback).(T)
		return
	}

	var err error
	switch any(dst).(type) {
	case *time.Duration:
		var val time.Duration
		val, err = time.ParseDuration(s)
		*dst = any(val).(T)
	case *int:
		_, err = fmt.Sscan(s, dst)
	case *string:
		*dst = any(s).(T)
	default:
		t.Fatalf("unsupported type: %T", dst)
	}
	require.NoError(t, err)
	t.Logf("%s: %v (from env)", name, *dst)
}
