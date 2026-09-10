// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package civisibility_failnow

import (
	"os"
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/require"

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

	validateFailNowEvents(ciVisibilityPayloads.Events())
	os.Exit(0)
}

func TestFailNowDoesNotTearDownCIVisibility(t *testing.T) {
	t.Run("Require", func(t *testing.T) {
		require.FailNow(t, "require failure")
	})
	t.Run("FailNow", func(t *testing.T) {
		t.FailNow()
	})
	t.Run("Fatal", func(t *testing.T) {
		t.Fatal("fatal failure")
	})
	t.Run("Fatalf", func(t *testing.T) {
		t.Fatalf("fatalf %s", "failure")
	})
	t.Run("After", func(t *testing.T) {})
}

func validateFailNowEvents(events civisibilitytest.Events) {
	events.CheckEventsByType("test_session_end", 1).
		CheckEventsByTagAndValue("test.status", "fail", 1).
		CheckEventsByTagAndValue("error.type", "ExitCode", 1).
		CheckEventsByTagAndValue("error.message", "exit code is not zero.", 1).
		CheckEventsByMetricAndValue("test.exit_code", 1, 1)
	events.CheckEventsByType("test_module_end", 1)
	events.CheckEventsByType("test_suite_end", 1)

	testEvents := events.CheckEventsByType("test", 6)
	parent := testEvents.
		CheckEventsByResourceName("testing_test.go.TestFailNowDoesNotTearDownCIVisibility", 1).
		CheckEventsByTagAndValue("test.status", "fail", 1)
	requireFailure := testEvents.
		CheckEventsByResourceName("testing_test.go.TestFailNowDoesNotTearDownCIVisibility/Require", 1).
		CheckEventsByTagAndValue("test.status", "fail", 1).
		CheckEventsByTagAndValue("error.type", "Errorf", 1)
	failNow := testEvents.
		CheckEventsByResourceName("testing_test.go.TestFailNowDoesNotTearDownCIVisibility/FailNow", 1).
		CheckEventsByTagAndValue("test.status", "fail", 1).
		CheckEventsByTagAndValue("test.final_status", "fail", 1).
		CheckEventsByTagAndValue("error.type", "FailNow", 1).
		CheckEventsByTagAndValue("error.message", "failed test", 1)
	fatal := testEvents.
		CheckEventsByResourceName("testing_test.go.TestFailNowDoesNotTearDownCIVisibility/Fatal", 1).
		CheckEventsByTagAndValue("test.status", "fail", 1).
		CheckEventsByTagAndValue("test.final_status", "fail", 1).
		CheckEventsByTagAndValue("error.type", "Fatal", 1).
		CheckEventsByTagAndValue("error.message", "fatal failure", 1)
	fatalf := testEvents.
		CheckEventsByResourceName("testing_test.go.TestFailNowDoesNotTearDownCIVisibility/Fatalf", 1).
		CheckEventsByTagAndValue("test.status", "fail", 1).
		CheckEventsByTagAndValue("test.final_status", "fail", 1).
		CheckEventsByTagAndValue("error.type", "Fatalf", 1).
		CheckEventsByTagAndValue("error.message", "fatalf failure", 1)
	after := testEvents.
		CheckEventsByResourceName("testing_test.go.TestFailNowDoesNotTearDownCIVisibility/After", 1).
		CheckEventsByTagAndValue("test.status", "pass", 1)

	testEvents.Except(parent, requireFailure, failNow, fatal, fatalf, after).HasCount(0)
}
