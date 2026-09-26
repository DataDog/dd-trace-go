// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package emptysession

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
)

const childEnv = "DD_EMPTY_SESSION_FIXTURE"

type result struct {
	ExitCode   int
	Tests      int
	Modules    int
	Sessions   int
	Status     any
	Reason     any
	SkipReason any
}

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "" {
		os.Exit(m.Run())
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/setting") {
			_, _ = w.Write([]byte(`{"data":{"id":"settings","type":"ci_app_libraries_tests_settings","attributes":{}}}`))
		} else {
			_, _ = w.Write([]byte("{}"))
		}
	}))
	defer server.Close()
	for key, value := range map[string]string{
		"DD_CIVISIBILITY_AGENTLESS_ENABLED":    "true",
		"DD_CIVISIBILITY_AGENTLESS_URL":        server.URL,
		"DD_API_KEY":                           "synthetic",
		"DD_GIT_REPOSITORY_URL":                "https://example.invalid/example.git",
		"DD_GIT_COMMIT_SHA":                    "0123456789abcdef0123456789abcdef01234567",
		"DD_CIVISIBILITY_LOGS_ENABLED":         "false",
		"DD_INSTRUMENTATION_TELEMETRY_ENABLED": "false",
	} {
		_ = os.Setenv(key, value)
	}
	mt := integrations.InitializeCIVisibilityMock()
	r := result{ExitCode: gotesting.RunM(m)}
	for _, span := range mt.FinishedSpans() {
		switch span.Tag(ext.SpanType) {
		case constants.SpanTypeTest:
			r.Tests++
		case constants.SpanTypeTestModule:
			r.Modules++
		case constants.SpanTypeTestSession:
			r.Sessions++
			r.Status = span.Tag(constants.TestStatus)
			r.Reason = span.Tag("test.session.empty_reason")
			r.SkipReason = span.Tag(constants.TestSkipReason)
		}
	}
	data, err := json.Marshal(r)
	if err != nil {
		panic(err)
	}
	fmt.Printf("CI_RESULT=%s\n", data)
	// The parent validates the native exit code and the emitted spans.
}

func TestSessionStatus(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		status   string
		tests    int
		exitCode int
	}{
		{"no-match", []string{"-test.run=^TestMissing$"}, "skip", 0, 0},
		{"skip-all", []string{"-test.run=^TestPassingFixture$", "-test.skip=Fixture"}, "skip", 0, 0},
		{"list-only", []string{"-test.list=Fixture"}, "skip", 0, 0},
		{"passing", []string{"-test.run=^TestPassingFixture$"}, "pass", 1, 0},
		{"failing", []string{"-test.run=^TestFailingFixture$"}, "fail", 1, 1},
		{"benchmark-only", []string{"-test.run=^$", "-test.bench=^BenchmarkFixture$", "-test.benchtime=1x"}, "pass", 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], tc.args...)
			cmd.Env = append(os.Environ(), childEnv+"=1")
			output, err := cmd.CombinedOutput()
			if tc.exitCode == 0 {
				require.NoError(t, err, "%s", output)
			} else {
				var exitErr *exec.ExitError
				require.ErrorAs(t, err, &exitErr, "%s", output)
				require.Equal(t, tc.exitCode, exitErr.ExitCode())
			}
			var r result
			found := false
			for _, line := range strings.Split(string(output), "\n") {
				if strings.HasPrefix(line, "CI_RESULT=") {
					require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "CI_RESULT=")), &r))
					found = true
				}
			}
			require.True(t, found, "%s", output)
			t.Logf("%s", output)
			require.Equal(t, tc.exitCode, r.ExitCode)
			require.Equal(t, 1, r.Sessions)
			require.Equal(t, tc.tests, r.Tests)
			require.Equal(t, tc.status, r.Status)
			if tc.status == "skip" {
				require.Zero(t, r.Modules)
				require.Equal(t, "zero_tests", r.Reason)
				require.Equal(t, "No tests or benchmarks ran", r.SkipReason)
			} else {
				require.Equal(t, 1, r.Modules)
				require.Nil(t, r.Reason)
				require.Nil(t, r.SkipReason)
			}
		})
	}
}

func TestPassingFixture(t *testing.T) {
	if os.Getenv(childEnv) == "" {
		t.Skip("subprocess fixture")
	}
}

func TestFailingFixture(t *testing.T) {
	if os.Getenv(childEnv) == "" {
		t.Skip("subprocess fixture")
	}
	t.Error("synthetic failure")
}

func BenchmarkFixture(b *testing.B) {
	for b.Loop() {
	}
}
