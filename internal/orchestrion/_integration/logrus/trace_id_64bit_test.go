// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package logrus

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/harness"
)

const log128BitsEnv = "DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED"

type traceID64BitLogger struct {
	TestCaseNewLogger
}

func (tc *traceID64BitLogger) Run(ctx context.Context, t *testing.T) {
	runTest(ctx, t, tc.logs, tc.Log, false)
}

// The hook reads the flag at package init, so the test binary re-runs this
// test in a child process with the flag set from the start.
func TestTraceID64BitLogging(t *testing.T) {
	if v, _ := os.LookupEnv(log128BitsEnv); v == "false" {
		harness.Run(t, new(traceID64BitLogger))
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestTraceID64BitLogging$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), log128BitsEnv+"=false")
	out, err := cmd.CombinedOutput()
	t.Logf("child process output:\n%s", out)
	require.NoError(t, err)
	require.Contains(t, string(out), "--- PASS: TestTraceID64BitLogging")
}
