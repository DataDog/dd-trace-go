// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/stableconfig"
)

var (
	startOnce sync.Once
	startErr  error // preserved from the first Start call; returned on subsequent calls
)

// init intercepts the monitor child process as early as possible — before any
// user package init() functions execute. This prevents app-level side-effects
// (DB connections, gRPC dials, signal handlers) from running in the lightweight
// monitor. When the crashtracker package is imported (e.g. via orchestrion's
// injected import), this init fires before the user's package inits.
//
// The monitor's configuration comes from monitorConfigFromEnv, which reads the
// internal DD_CRASHTRACKING_MONITOR_* variables set by spawnMonitor: this is how
// options passed to Start in the application process (WithService, WithAPIKey,
// etc.) reach the monitor, which is a separate process and cannot see them
// directly. Because init always wins this race, start's own monitor-role branch
// is unreachable and intentionally does not exist.
func init() {
	if isMonitorProcess() {
		runMonitor(monitorConfigFromEnv()) // never returns; calls os.Exit
	}
}

// Start initialises the crashtracker. It must be called as early as possible in main().
//
// Start spawns a monitor child process, registers the crash pipe via
// runtime/debug.SetCrashOutput, and returns so the application continues normally.
// There is no corresponding Stop: process exit alone closes the pipe, which is
// all the cleanup the monitor needs (it reads EOF and exits without a report).
// An explicit unregister-and-close step is unnecessary and actively harmful if
// deferred, since deferred functions run during panic unwinding — before the
// runtime writes the crash dump — which would disable reporting for the most
// common crash: an unrecovered panic. See the package doc for the full example.
//
// Start is idempotent: subsequent calls after the first are no-ops.
func Start(opts ...Option) error {
	startOnce.Do(func() { startErr = start(opts...) })
	return startErr
}

func start(opts ...Option) error {
	cfg := defaultConfig()
	for _, o := range opts {
		o(cfg)
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
