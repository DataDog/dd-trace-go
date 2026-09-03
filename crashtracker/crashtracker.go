// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/stableconfig"
)

var (
	startOnce sync.Once
	startErr  error // preserved from the first Start call; returned on subsequent calls
)

// init intercepts the monitor child process as early as package initialization
// allows. It runs before main and therefore before Start, but Go does not
// guarantee it runs before init functions in independently imported packages.
// See the package documentation for that limitation.
//
// The monitor's configuration comes from monitorConfigFromEnv, which reads the
// internal DD_CRASHTRACKING_MONITOR_* variables set by spawnMonitor: this is how
// options passed to Start in the application process (WithService, WithAPIKey,
// etc.) reach the monitor, which is a separate process and cannot see them
// directly. Because package initialization completes before main begins, Start's
// own monitor-role branch is unreachable and intentionally does not exist.
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
		service: resolveService(),
		env:     env.Get("DD_ENV"),
		version: env.Get("DD_VERSION"),
		site:    env.Get("DD_SITE"),
		apiKey:  env.Get("DD_API_KEY"),
	}
}

// resolveService resolves the service name when Start runs before the tracer
// does. globalconfig.ServiceName only returns a value the tracer previously
// stored via SetServiceName; it never reads DD_SERVICE itself, so a
// crashtracker.Start call in main before tracer.Start would otherwise drop
// the service tag entirely on every report.
//
// The final fallback matches tracer.Start's own default (ddtrace/tracer/option.go)
// rather than a literal "unknown": under the documented lifecycle, crashtracker.Start
// runs before tracer.Start sets that default, so without this the two would
// independently pick different values for the same process and crash reports
// would fail to correlate with that service's traces.
func resolveService() string {
	if s := globalconfig.ServiceName(); s != "" {
		return s
	}
	if s := env.Get("DD_SERVICE"); s != "" {
		return s
	}
	return filepath.Base(os.Args[0])
}
