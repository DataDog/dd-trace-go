// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package civisibility_failnow_benchmark

import (
	"os"
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/civisibilitytest"
)

var ciVisibilityPayloads *civisibilitytest.Payloads

func TestMain(m *testing.M) {
	if !built.WithOrchestrion {
		panic("Orchestrion is not enabled, please run this test with orchestrion")
	}

	settings := net.SettingsResponseData{}
	server, payloads, restore := civisibilitytest.StartMockServer(settings)
	defer restore()
	_ = server
	ciVisibilityPayloads = payloads

	exitCode := m.Run()
	if exitCode != 1 {
		panic("expected m.Run to fail with exit code 1")
	}

	validateBenchmarkEvents(ciVisibilityPayloads.Events())
	os.Exit(0)
}

func BenchmarkFailNow(b *testing.B) {
	b.FailNow()
}

func BenchmarkAfterFailNow(b *testing.B) {
	for b.Loop() {
	}
}

func validateBenchmarkEvents(events civisibilitytest.Events) {
	events.CheckEventsByType("test_session_end", 1).
		CheckEventsByTagAndValue("test.status", "fail", 1).
		CheckEventsByTagAndValue("error.type", "ExitCode", 1).
		CheckEventsByTagAndValue("error.message", "exit code is not zero.", 1).
		CheckEventsByMetricAndValue("test.exit_code", 1, 1)
	events.CheckEventsByType("test_module_end", 1)
	events.CheckEventsByType("test_suite_end", 1)

	testEvents := events.CheckEventsByType("test", 2)
	failNow := testEvents.
		CheckEventsByResourceName("benchmark_test.go.BenchmarkFailNow", 1).
		CheckEventsByTagAndValue("test.status", "fail", 1).
		CheckEventsByTagAndValue("error.type", "FailNow", 1).
		CheckEventsByTagAndValue("error.message", "failed test", 1)
	after := testEvents.
		CheckEventsByResourceName("benchmark_test.go.BenchmarkAfterFailNow", 1).
		CheckEventsByTagAndValue("test.status", "pass", 1)

	testEvents.Except(failNow, after).HasCount(0)
}
