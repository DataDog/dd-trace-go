// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/stableconfig"
)

var (
	startOnce sync.Once
	startErr  error // preserved from the first Start call; returned on subsequent calls

	// activePipe holds the write end of the crash pipe registered with
	// runtime/debug.SetCrashOutput. It is set when the monitor is spawned and
	// released by Stop.
	activePipe atomic.Pointer[os.File]
)

// init intercepts the monitor child process as early as possible — before any
// user package init() functions execute. This prevents app-level side-effects
// (DB connections, gRPC dials, signal handlers) from running in the lightweight
// monitor. When the crashtracker package is imported (e.g. via orchestrion's
// injected import), this init fires before the user's package inits.
func init() {
	if isMonitorProcess() {
		runMonitor(defaultConfig()) // never returns; calls os.Exit
	}
}

// Start initialises the crashtracker. It must be called as early as possible in main().
//
// In the monitor child process (identified by the DD_CRASHTRACKING_IS_MONITOR_PROCESS
// environment variable), Start hijacks control: it reads the crash pipe, processes the
// report, and calls os.Exit. It never returns in that case.
//
// In the application process, Start spawns the monitor child, wires SetCrashOutput, and
// returns so the application continues normally.
//
// Start is idempotent: subsequent calls after the first are no-ops.
func Start(opts ...Option) error {
	startOnce.Do(func() { startErr = start(opts...) })
	return startErr
}

// Stop disables crash output capture. It is a best-effort call and may be deferred
// from main() to ensure the monitor is released on clean exit.
func Stop() {
	// Best-effort cleanup: unregister the crash output. Any error here is not
	// actionable because the process is on its way down.
	_ = debug.SetCrashOutput(nil, debug.CrashOptions{}) //nolint:errcheck
	if f := activePipe.Swap(nil); f != nil {
		_ = f.Close()
	}
}

func start(opts ...Option) error {
	cfg := defaultConfig()
	for _, o := range opts {
		o(cfg)
	}

	if isMonitorProcess() {
		runMonitor(cfg) // never returns
	}

	if !cfg.enabled {
		return nil
	}

	return spawnMonitor(cfg)
}

func defaultConfig() *config {
	enabled, _, _ := stableconfig.Bool("DD_CRASHTRACKING_ENABLED", true)
	return &config{
		enabled: enabled,
		service: globalconfig.ServiceName(),
		env:     env.Get("DD_ENV"),
		version: env.Get("DD_VERSION"),
		site:    env.Get("DD_SITE"),
		apiKey:  env.Get("DD_API_KEY"),
	}
}
