//go:build !race

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import "testing"

const (
	quarantinedRacePIDDirEnv            = "DD_TEST_QUARANTINED_RACE_PID_DIR"
	quarantinedRaceStateDirEnv          = "DD_TEST_QUARANTINED_RACE_STATE_DIR"
	quarantinedRaceCustomTestMainEnv    = "DD_TEST_QUARANTINED_RACE_CUSTOM_TESTMAIN"
	quarantinedRaceCustomTestMainPIDEnv = "DD_TEST_QUARANTINED_RACE_CUSTOM_TESTMAIN_PID"
)

func quarantinedRaceScenarioAvailable() bool { return false }

func unquarantinedRaceFixtureSelected() bool { return false }

func quarantinedRaceInProcessFixtureSelected() bool { return false }

func acquireQuarantinedRaceCustomTestMainResource() func() { return func() {} }

func runQuarantinedRaceTests(*testing.M, string) {
	panic("quarantined race scenario requires a race-enabled test binary")
}

func runUnquarantinedRaceFixture(*testing.M) {
	panic("unquarantined race scenario requires a race-enabled test binary")
}

func runQuarantinedRaceCustomTestMainTests(*testing.M) {
	panic("quarantined race custom TestMain scenario requires a race-enabled test binary")
}
