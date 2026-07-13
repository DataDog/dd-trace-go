// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker_test

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/crashtracker"
)

// Start uses a package-level sync.Once, so it fires at most once per process
// and cannot be reset from an external test package. A Start call that actually
// spawns the monitor would re-exec the test binary, which is not hermetic.
//
// Every Start call below therefore passes WithEnabled(false): the enabled gate
// short-circuits before spawnMonitor, so no monitor child is ever launched no
// matter which test happens to win the Once. The remaining behaviour under test
// is the option plumbing, the enabled gate, and idempotency. Stop is exercised
// independently of the Once.

func TestStartRespectsDisabled(t *testing.T) {
	// A disabled crashtracker must return nil without spawning a monitor.
	if err := crashtracker.Start(crashtracker.WithEnabled(false)); err != nil {
		t.Fatalf("Start(WithEnabled(false)) = %v, want nil", err)
	}
	t.Cleanup(crashtracker.Stop)
}

func TestStartIdempotent(t *testing.T) {
	// The first winning call runs start; every later call is a no-op returning
	// the same (nil) result. Calling twice must not error or panic.
	if err := crashtracker.Start(crashtracker.WithEnabled(false)); err != nil {
		t.Fatalf("Start (first call) = %v, want nil", err)
	}
	if err := crashtracker.Start(crashtracker.WithEnabled(false)); err != nil {
		t.Fatalf("Start (second call) = %v, want nil", err)
	}
	t.Cleanup(crashtracker.Stop)
}

func TestStartWithOptions(t *testing.T) {
	// Options must be accepted and applied without error. WithEnabled(false)
	// keeps the call from spawning a monitor child.
	err := crashtracker.Start(
		crashtracker.WithService("mysvc"),
		crashtracker.WithEnv("prod"),
		crashtracker.WithVersion("1.2.3"),
		crashtracker.WithEnabled(false),
	)
	if err != nil {
		t.Fatalf("Start(WithService, WithEnv, WithVersion, WithEnabled(false)) = %v, want nil", err)
	}
	t.Cleanup(crashtracker.Stop)
}

func TestStopWithoutStart(t *testing.T) {
	// Stop must be safe to call when Start was never invoked (or won by another
	// test): it unregisters crash output and releases the pipe if present.
	crashtracker.Stop()
}

func TestStopIsIdempotent(t *testing.T) {
	// Repeated Stop calls must not panic.
	crashtracker.Stop()
	crashtracker.Stop()
}
