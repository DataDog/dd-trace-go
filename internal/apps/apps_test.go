package apps

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
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

func TestMemoryLeak(t *testing.T) {
	tc := EnvTestConfig(t)
	ti := tc.Launch(t, "memory-leak")
	defer ti.Stop(t)

	ctx, cancel := context.WithTimeout(context.Background(), tc.TotalDuration)
	defer cancel()

	ticker := time.NewTicker(time.Second / time.Duration(tc.RPS))
	defer ticker.Stop()

	endpoints := []string{"/foo", "/bar"}
	var eg errgroup.Group
loop:
	for {
		select {
		case <-ticker.C:
			for _, endpoint := range endpoints {
				url := "http://" + ti.HostPort + endpoint
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

func EnvTestConfig(t *testing.T) (tc TestConfig) {
	if os.Getenv("DD_TEST_APPS_ENABLED") != "true" {
		t.Skip("set DD_TEST_APPS_ENABLED=true env var to run")
	}

	var err error
	tc.TotalDuration = 70 * time.Second
	if s := os.Getenv("DD_TEST_APPS_TOTAL_DURATION"); s != "" {
		tc.TotalDuration, err = time.ParseDuration(s)
		require.NoError(t, err)
	}

	tc.ProfilePeriod = 10 * time.Second // enough for 3 profiles per version
	if s := os.Getenv("DD_TEST_APPS_PROFILE_PERIOD"); s != "" {
		tc.ProfilePeriod, err = time.ParseDuration(s)
		require.NoError(t, err)
	}

	tc.RPS = 5
	if s := os.Getenv("DD_TEST_APPS_REQUESTS_PER_SECOND"); s != "" {
		_, err = fmt.Sscan(s, &tc.RPS)
		require.NoError(t, err)
	}

	tc.Tags = strings.TrimSpace(fmt.Sprintf(
		"%s run_id:%d DD_TEST_APPS_REQUESTS_PER_SECOND:%d DD_TEST_APPS_PROFILE_PERIOD:%s DD_TEST_APPS_TOTAL_DURATION:%s",
		os.Getenv("DD_TAGS"),
		rand.Uint64(),
		tc.RPS,
		tc.ProfilePeriod,
		tc.TotalDuration,
	))
	log.Printf("Using DD_TAGS: %s", tc.Tags)

	return
}

type TestConfig struct {
	TotalDuration time.Duration
	ProfilePeriod time.Duration
	RPS           int
	Tags          string
	Context       context.Context
}

func (tc *TestConfig) Launch(t *testing.T, app string, args ...string) (ti TestInstance) {
	// Start app
	if ti.HostPort == "" {
		ti.HostPort = "localhost:8080"
	}

	binPath := filepath.Join(os.TempDir(), app)
	defer os.Remove(binPath)

	// Launch test app as its own binary. This produces a more realistic looking
	// profile than running the workload from a TestXXX func.
	cmd := fmt.Sprintf(
		"go build -o %[1]s ./%[2]s && exec %[1]s -http %[3]s -period %[4]s %[5]s",
		binPath,
		app,
		ti.HostPort,
		tc.ProfilePeriod,
		strings.Join(args, " "),
	)
	proc := exec.Command("bash", "-c", cmd)
	proc.Env = append(os.Environ(), "DD_TAGS="+tc.Tags)
	r, w := io.Pipe()
	proc.Stdout = io.MultiWriter(w, os.Stdout)
	proc.Stderr = io.MultiWriter(w, os.Stderr)
	require.NoError(t, proc.Start())

	ti.wait = make(chan error, 1)
	go func() {
		err := proc.Wait()
		// Unblock scanner.Scan if app crashes before listening
		w.CloseWithError(err)
		ti.wait <- err
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

	ti.proc = proc
	return
}

type TestInstance struct {
	HostPort string
	wait     chan error
	proc     *exec.Cmd
}

func (ti *TestInstance) Stop(t *testing.T) {
	// Shutdown app
	ti.proc.Process.Signal(os.Interrupt)
	require.NoError(t, <-ti.wait)
}
