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
		cmd := exec.Command(os.Args[0], os.Args[1:]...)
		var buffer bytes.Buffer
		cmd.Stdout = &buffer
		cmd.Stderr = &buffer
		if log.DebugEnabled() {
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
		}
		cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%s", subtestScenarioEnv, scenario))

		fmt.Printf("\n**** [RUNNING SUBTEST SCENARIO: %s]\n", scenario)
		if err := cmd.Run(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				fmt.Printf("\n**** [SCENARIO %s FAILED WITH EXIT CODE: %d]\n", scenario, exitErr.ExitCode())
				fmt.Printf("**** [SCENARIO %s OUTPUT]\n%s\n", scenario, buffer.String())
				restoreEnv(constants.CIVisibilitySubtestFeaturesEnabled, prevDD, hadDD)
				os.Exit(exitErr.ExitCode())
			}
			fmt.Printf("failed to run scenario %s: %v\n", scenario, err)
			restoreEnv(constants.CIVisibilitySubtestFeaturesEnabled, prevDD, hadDD)
			os.Exit(1)
		}
		fmt.Printf("**** [SCENARIO %s COMPLETED]\n", scenario)
	}

	code := m.Run()

	restoreEnv(constants.CIVisibilitySubtestFeaturesEnabled, prevDD, hadDD)

	os.Exit(code)
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
