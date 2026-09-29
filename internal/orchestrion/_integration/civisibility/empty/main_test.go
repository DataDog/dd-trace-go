// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package empty

import (
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/civisibilitytest"
)

// Keep this package free of tests, benchmarks, fuzz targets, and examples so
// testing.M.Run executes an empty workload without any run filters.
func TestMain(m *testing.M) {
	if !built.WithOrchestrion {
		panic("Orchestrion is not enabled, please run this test with orchestrion")
	}

	_, payloads, restore := civisibilitytest.StartMockServerWithOptions(
		civisibilitytest.WithEnv("DD_CIVISIBILITY_GIT_UPLOAD_ENABLED", "false"),
		civisibilitytest.WithEnv("DD_CIVISIBILITY_LOGS_ENABLED", "false"),
		civisibilitytest.WithEnv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false"),
	)
	defer restore()

	if exitCode := m.Run(); exitCode != 0 {
		panic("expected an empty test binary to exit successfully")
	}

	events := payloads.Events().HasCount(1)
	events.CheckEventsByType("test_session_end", 1).
		CheckEventsByMetricAndValue("test.exit_code", 0, 1).
		CheckEventsByTagAndValue("test.status", "skip", 1).
		CheckEventsByTagAndValue("test.skip_reason", "No tests or benchmarks ran", 1).
		CheckEventsByTagAndValue("test.session.empty_reason", "zero_tests", 1)
	events.CheckEventsByType("test", 0)
	events.CheckEventsByType("test_module_end", 0)
	events.CheckEventsByType("test_suite_end", 0)
}
