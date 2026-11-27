// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build windows

package crashtracker

import (
	"os"
	"os/signal"
	"testing"
)

// The supervisor must not undo the application's own Ctrl-C handling when the
// re-exec fails and the process continues as the application.
func TestSupervisorSignalsKeepApplicationInterruptHandler(t *testing.T) {
	app := make(chan os.Signal, 1)
	signal.Notify(app, os.Interrupt)
	t.Cleanup(func() { signal.Stop(app) })

	start, stop := supervisorSignals(false)
	start(nil)
	if signal.Ignored(os.Interrupt) {
		t.Fatal("the supervisor ignores os.Interrupt")
	}
	stop()
	if signal.Ignored(os.Interrupt) {
		t.Fatal("stop left os.Interrupt ignored")
	}
}
