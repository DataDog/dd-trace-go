// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package skippedpkg simulates an implementation package that internally
// delegates through the same protected function names with a variable message
// argument (e.g. internal/telemetry/log itself).
//
// When "skippedpkg" is added to the skipPkgs list the analyzer must produce
// no diagnostics for any call in this file, even though the message is not a
// compile-time constant. This tests the skip-package mechanism.
package skippedpkg

import telemetrylog "example.com/faketelemetrylog"

func delegate(message string) {
	// Intentional internal delegation with a variable message — these must not
	// be flagged when this package is in the skip list.
	telemetrylog.Debug(message)
	telemetrylog.Warn(message)
	telemetrylog.Error(message)

	logger := telemetrylog.With()
	logger.Debug(message)
	logger.Warn(message)
	logger.Error(message)
}
