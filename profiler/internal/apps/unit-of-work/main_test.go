// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestUnitOfWork(t *testing.T) {
	if os.Getenv("DD_TEST_APPS_ENABLED") != "true" {
		t.Skip("set DD_TEST_APPS_ENABLED=true env var to run")
	}
	var err error

	// The test executes for totalDuration as shown below. The first half of the
	// time goes to running the app as v1 and the other half to run it as v2 with
	// a different workload. Each version produces one profile every
	// profilePeriod.
	//
	// | totalDuration                                            |
	// | v1                          | v2                         |
	// | profile 1 | profile 2 | ... | profile 1 | profile 2 | ...|
	// ------------------------------------------> time

	totalDuration := 70 * time.Second // default
	if s := os.Getenv("DD_TEST_APPS_TOTAL_DURATION"); s != "" {
		totalDuration, err = time.ParseDuration(s)
		require.NoError(t, err)
	}

	profilePeriod := 10 * time.Second // enough for 3 profiles per version
	if s := os.Getenv("DD_TEST_APPS_PROFILE_PERIOD"); s != "" {
		profilePeriod, err = time.ParseDuration(s)
		require.NoError(t, err)
	}

	rps := 5
	if s := os.Getenv("DD_TEST_APPS_REQUESTS_PER_SECOND"); s != "" {
		_, err := fmt.Sscan(s, &rps)
		require.NoError(t, err)
	}

	ddTags := strings.TrimSpace(fmt.Sprintf(
		"%s run_id:%d DD_TEST_APPS_REQUESTS_PER_SECOND:%d DD_TEST_APPS_PROFILE_PERIOD:%s DD_TEST_APPS_TOTAL_DURATION:%s",
		os.Getenv("DD_TAGS"),
		rand.Uint64(),
		rps,
		profilePeriod,
		totalDuration,
	))
	log.Printf("Using DD_TAGS: %s", ddTags)

	versions := []struct {
		Version   string
		Endpoints []string // each endpoint is requested at the rps rate
	}{
		{"v1", []string{"/foo", "/bar"}},
		{"v2", []string{"/foo", "/bar", "/bar"}},
	}
	for _, version := range versions {
		t.Run(version.Version, func(t *testing.T) {
			app := App{
				DDTags:         ddTags,
				ProfilePeriod:  profilePeriod,
				ServiceVersion: version.Version,
			}
			app.Start(t)
			defer app.Stop(t)

			stop := closeAfter(totalDuration / time.Duration(len(versions)))

			var eg errgroup.Group
			for _, endpoint := range version.Endpoints {
				url := "http://" + app.HostPort + endpoint
				eg.Go(func() error {
					ticker := time.Tick(time.Second / time.Duration(rps))
					for {
						select {
						case <-ticker:
							req, err := http.Get(url)
							if err != nil {
								return err
							}
							req.Body.Close()
						case <-stop:
							return nil
						}
					}
				})
			}
			require.NoError(t, eg.Wait())
		})
	}
}

// closeAfter returns a channel that is closed after the given amount of time.
func closeAfter(dt time.Duration) <-chan struct{} {
	ch := make(chan struct{})
	time.AfterFunc(dt, func() { close(ch) })
	return ch
}

type App struct {
	ServiceVersion string
	ProfilePeriod  time.Duration
	DDTags         string
	HostPort       string

	proc *exec.Cmd
}

// Run launches the test app.
func (a *App) Start(t *testing.T) {
	// Start app
	if a.HostPort == "" {
		a.HostPort = "localhost:8080"
	}
	cmd := fmt.Sprintf(
		"go build && exec ./unit-of-work -http %s -version %s -period %s",
		a.HostPort,
		a.ServiceVersion,
		a.ProfilePeriod,
	)
	proc := exec.Command("bash", "-c", cmd)
	proc.Env = append(os.Environ(), "DD_TAGS="+a.DDTags)
	r, w := io.Pipe()
	proc.Stdout = io.MultiWriter(w, os.Stdout)
	proc.Stderr = io.MultiWriter(w, os.Stderr)
	require.NoError(t, proc.Start())

	// Wait until app is ready
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "Listening on:") {
			break
		}
	}
	// Keep draining r to avoid blocking
	go io.Copy(io.Discard, r)

	a.proc = proc
}

// Stop terminates the app.
func (a *App) Stop(t *testing.T) {
	// Shutdown app
	a.proc.Process.Signal(os.Interrupt)
	require.NoError(t, a.proc.Wait())
}
