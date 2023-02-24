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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUnitOfWork(t *testing.T) {
	if os.Getenv("TEST_APPS_RUN") != "true" {
		t.Skip("set TEST_APPS_RUN=true env var to run")
	}

	app := App{
		DDTags: strings.TrimSpace(fmt.Sprintf(
			"%s run_id:%d",
			os.Getenv("DD_TAGS"),
			rand.Uint64()),
		),
		ProfilePeriod: 60 * time.Second,
	}
	log.Printf("Using DD_TAGS=%q", app.DDTags)
	testDuration := app.ProfilePeriod + 5*time.Second

	t.Run("v1", func(t *testing.T) {
		app.ServiceVersion = "v1"
		for i := 0; i < 3; i++ {
			// Request /foo and /bar
			app.Run(t, func(hostPort string) {
				start := time.Now()
				for i := 0; i < 100; i++ {
					requestEndpoints(t, hostPort, "/foo", "/bar")
				}
				time.Sleep(testDuration - time.Since(start))
			})
		}
	})

	t.Run("v2", func(t *testing.T) {
		app.ServiceVersion = "v2"
		for i := 0; i < 3; i++ {
			// Request /bar twice as much as /foo
			app.Run(t, func(hostPort string) {
				start := time.Now()
				for i := 0; i < 100; i++ {
					requestEndpoints(t, hostPort, "/foo", "/bar", "/bar")
				}
				time.Sleep(testDuration - time.Since(start))
			})
		}
	})
}

type App struct {
	ServiceVersion string
	ProfilePeriod  time.Duration
	DDTags         string
}

// Run launches the unit of work test app and then call fn in order to put some
// load on the app. When fn returns, the app is terminated.
func (a *App) Run(t *testing.T, fn func(hostPort string)) {
	// Start app
	hostPort := "localhost:8080"
	cmd := fmt.Sprintf(
		"go build && exec ./unit-of-work -http %s -version %s -period %s",
		hostPort,
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

	// Invoke callback
	fn(hostPort)

	// Shutdown app
	proc.Process.Signal(os.Interrupt)
	require.NoError(t, proc.Wait())
}

// requestEndpoints requests the given endpoints at hostPort concurrently.
func requestEndpoints(t *testing.T, hostPort string, endpoints ...string) {
	var wg sync.WaitGroup
	for _, endpoint := range endpoints {
		endpoint := endpoint
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.Get("http://" + hostPort + endpoint)
			require.NoError(t, err)
			req.Body.Close()
		}()
	}
	wg.Wait()
}
