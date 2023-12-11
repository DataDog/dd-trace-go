// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package apps

import (
	"bufio"
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
	// Keep draining r to avoid blocking the app
	go io.Copy(io.Discard, r)

	// Check startup succeeded
	require.True(t, listening, "app failed to start")

	p.proc = proc
	return
}

type process struct {
	HostPort string
	wait     chan error
	proc     *exec.Cmd
}

func (ti *process) Stop(t *testing.T) {
	// Shutdown app
	ti.proc.Process.Signal(os.Interrupt)
	require.NoError(t, <-ti.wait)
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
