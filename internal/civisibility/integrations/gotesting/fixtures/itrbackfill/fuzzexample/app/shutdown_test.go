// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/fixtures/itrbackfill/internal/mockci"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
)

func FuzzFatalRoot(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "fatal-child" {
		f.Skip("fatal lifecycle fixture requires its subprocess harness")
	}
	panic("root panic sentinel")
}

func FuzzFatalSeed(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "fatal-child" {
		f.Skip("fatal lifecycle fixture requires its subprocess harness")
	}
	f.Add(0)
	f.Add(1)
	gotesting.GetFuzz(f).Fuzz(func(t *testing.T, seed int) {
		if seed == 0 {
			panic("seed panic sentinel")
		}
		fmt.Println("NEXT_SEED_EXECUTED")
	})
}

// Keep the intake in the parent: an unquarantined fuzz panic terminates the
// child from testing's runner goroutine rather than unwinding TestMain.
func TestFuzzFatalShutdown(t *testing.T) {
	if os.Getenv("DD_FUZZ_EXAMPLE_MODE") == "" {
		t.Skip("fixture requires the fuzz/example harness")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"root", "seed", "managed-root", "managed-seed"} {
		t.Run(scenario, func(t *testing.T) {
			root := "FuzzFatalRoot"
			fn := FuzzFatalRoot
			if strings.HasSuffix(scenario, "seed") {
				root, fn = "FuzzFatalSeed", FuzzFatalSeed
			}
			managed := strings.HasPrefix(scenario, "managed-")
			settings := net.SettingsResponseData{SubtestFeaturesEnabled: true}
			var management *net.TestManagementTestsResponseDataModules
			name := root
			if strings.HasSuffix(scenario, "seed") {
				name += "/seed#0"
			}
			if managed {
				settings.TestManagement.Enabled = true
				source := runtime.FuncForPC(reflect.ValueOf(fn).Pointer())
				module, suite := utils.GetModuleAndSuiteName(source.Entry())
				management = &net.TestManagementTestsResponseDataModules{Modules: map[string]net.TestManagementTestsResponseDataSuites{
					module: {Suites: map[string]net.TestManagementTestsResponseDataTests{
						suite: {Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
							name: {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Quarantined: true}},
						}},
					}},
				}}
			}
			server := mockci.StartWithTestManagement(settings, nil, nil, management)
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "-test.run=^"+root+"$", "-test.timeout=15s")
			cmd.Env = append(os.Environ(), "DD_FUZZ_EXAMPLE_SCENARIO=fatal-child")
			output, err := cmd.CombinedOutput()
			if (err == nil) != managed {
				t.Fatalf("exit = %v; output: %s", err, output)
			}
			if !managed {
				exit, ok := err.(*exec.ExitError)
				if !ok || exit.ExitCode() != 2 {
					t.Fatalf("panic exit = %v, want 2: %s", err, output)
				}
			}
			if strings.Contains(string(output), "NEXT_SEED_EXECUTED") != (scenario == "managed-seed") {
				t.Fatalf("unexpected later seed: %s", output)
			}
			if !server.HasEventResourceSuffixMeta(name, constants.TestStatus, constants.TestStatusFail) {
				t.Fatalf("failure not flushed: %+v", server.Events())
			}
			final := constants.TestStatusFail
			if managed {
				final = constants.TestStatusSkip
			}
			if !server.HasEventResourceSuffixMeta(name, constants.TestFinalStatus, final) {
				t.Fatalf("wrong final status: %+v", server.Events())
			}
			rootStatus := constants.TestStatusFail
			if scenario == "managed-seed" {
				rootStatus = constants.TestStatusPass
			}
			if !server.HasEventResourceSuffixMeta(root, constants.TestStatus, rootStatus) {
				t.Fatalf("root not flushed: %+v", server.Events())
			}
			count := 1
			if scenario == "seed" {
				count = 2
			}
			if scenario == "managed-seed" {
				count = 3
			}
			if got := server.EventTypeCount(constants.SpanTypeTest); got != count {
				t.Fatalf("test events = %d, want %d: %+v", got, count, server.Events())
			}
			for _, kind := range []string{constants.SpanTypeTestSession, constants.SpanTypeTestModule, constants.SpanTypeTestSuite} {
				if got := server.EventTypeCount(kind); got != 1 {
					t.Fatalf("%s count = %d, want 1: %+v", kind, got, server.Events())
				}
			}
		})
	}
}
