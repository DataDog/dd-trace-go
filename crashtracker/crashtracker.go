// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import "sync"

var startOnce sync.Once

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
	var err error
	startOnce.Do(func() {
		err = start(opts...)
	})
	return err
}

// Stop disables crash output capture. It is a best-effort call and may be deferred
// from main() to ensure the monitor is released on clean exit.
func Stop() {
	// WS-A implements this
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
	// WS-A fills this in properly; placeholder to compile
	return &config{enabled: true}
}
