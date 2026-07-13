// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import "os"

// monitorEnvVar is the environment variable set on the monitor child process.
const monitorEnvVar = "DD_CRASHTRACKING_IS_MONITOR_PROCESS"

// isMonitorProcess reports whether the current process is the monitor child.
func isMonitorProcess() bool {
	return os.Getenv(monitorEnvVar) == "1" //nolint:forbidigo
}

// runMonitor is the monitor-child entry point. It reads crash output from stdin,
// parses it, and uploads a report. It never returns.
func runMonitor(cfg *config) {
	panic("not implemented") // WS-A implements this
}

// spawnMonitor re-execs the current binary as a monitor child, sets up a pipe,
// and calls SetCrashOutput with the pipe write end.
func spawnMonitor(cfg *config) error {
	panic("not implemented") // WS-A implements this
}
