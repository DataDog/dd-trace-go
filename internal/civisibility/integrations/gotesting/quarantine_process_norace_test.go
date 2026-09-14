//go:build !race

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import "testing"

func quarantinedRaceIsolationFixtureSelected() bool { return false }

func runQuarantinedRaceIsolationFixture(*testing.M) {
	panic("quarantined race isolation fixture requires a race-enabled binary")
}

func TestQuarantinedRaceProcessRequiresRaceBuild(t *testing.T) {
	if quarantinedRaceProcessSupported() {
		t.Fatal("quarantined race process isolation must be disabled without -race")
	}
}
