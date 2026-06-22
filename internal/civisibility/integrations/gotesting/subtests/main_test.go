// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package subtests

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	subtestScenarioEnv = "SUBTEST_MATRIX_SCENARIO"
)

// TestMain forces subtest-specific features on and orchestrates scenario subprocesses so this suite exercises the flag-enabled paths.
func TestMain(m *testing.M) {
	prevDD, hadDD := os.LookupEnv(constants.CIVisibilitySubtestFeaturesEnabled)

	if scenario := os.Getenv(subtestScenarioEnv); scenario != "" {
		code := runMatrixScenario(m, scenario)
		restoreEnv(constants.CIVisibilitySubtestFeaturesEnabled, prevDD, hadDD)
		os.Exit(code)
	}

	for _, scenario := range matrixScenarioNames() {
		const maxInitRetries = 2
		for attempt := 0; ; attempt++ {
			cmd := exec.Command(os.Args[0], scenarioArgs(os.Args[1:])...)
			var buffer bytes.Buffer
			cmd.Stdout = &buffer
			cmd.Stderr = &buffer
			if log.DebugEnabled() {
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
			}
			cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%s", subtestScenarioEnv, scenario))

			fmt.Printf("\n**** [RUNNING SUBTEST SCENARIO: %s]\n", scenario)
			err := cmd.Run()
			if err == nil {
				fmt.Printf("**** [SCENARIO %s COMPLETED]\n", scenario)
				break
			}

			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				fmt.Printf("failed to run scenario %s: %v\n", scenario, err)
				restoreEnv(constants.CIVisibilitySubtestFeaturesEnabled, prevDD, hadDD)
				os.Exit(1)
			}

			// scenarioInitFailureExitCode means CI Visibility features were not initialised
			// (transient settings/management fetch failure).  Retry up to maxInitRetries times
			// so a genuine one-shot network hiccup doesn't fail the whole test suite.
			if exitErr.ExitCode() == scenarioInitFailureExitCode && attempt < maxInitRetries {
				fmt.Printf("**** [SCENARIO %s INIT FAILURE (attempt %d/%d), retrying]\n", scenario, attempt+1, maxInitRetries)
				fmt.Printf("**** [SCENARIO %s OUTPUT]\n%s\n", scenario, buffer.String())
				continue
			}

			fmt.Printf("\n**** [SCENARIO %s FAILED WITH EXIT CODE: %d]\n", scenario, exitErr.ExitCode())
			fmt.Printf("**** [SCENARIO %s OUTPUT]\n%s\n", scenario, buffer.String())
			restoreEnv(constants.CIVisibilitySubtestFeaturesEnabled, prevDD, hadDD)
			os.Exit(exitErr.ExitCode())
		}
	}

	code := m.Run()

	restoreEnv(constants.CIVisibilitySubtestFeaturesEnabled, prevDD, hadDD)

	os.Exit(code)
}

func scenarioArgs(args []string) []string {
	out := make([]string, 0, len(args)+1)
	for idx := 0; idx < len(args); idx++ {
		arg := args[idx]
		if arg == "-count" || arg == "-test.count" {
			idx++
			continue
		}
		if strings.HasPrefix(arg, "-count=") || strings.HasPrefix(arg, "-test.count=") {
			continue
		}
		out = append(out, arg)
	}
	return append(out, "-test.count=1")
}

func restoreEnv(key, value string, had bool) {
	if !had {
		if err := os.Unsetenv(key); err != nil {
			panic(err)
		}
		return
	}
	if err := os.Setenv(key, value); err != nil {
		panic(err)
	}
}
