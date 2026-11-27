// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/crashtracker"
)

func TestStartIdempotent(t *testing.T) {
	const outcomeEnv = "_CRASHTRACKER_START_OUTCOME"
	switch os.Getenv(outcomeEnv) {
	case "disabled":
		if err := crashtracker.Start(crashtracker.WithEnabled(false), crashtracker.WithService("test")); err != nil {
			t.Fatalf("disabled Start = %v, want nil", err)
		}
		t.Setenv("_DD_CRASHTRACKING_SUPERVISED_FD", fmt.Sprintf("%d:-1", os.Getppid()))
		if err := crashtracker.Start(crashtracker.WithEnabled(false)); err != nil {
			t.Fatalf("later Start = %v, want nil", err)
		}
		return
	case "error":
		t.Setenv("_DD_CRASHTRACKING_SUPERVISED_FD", fmt.Sprintf("%d:-1", os.Getppid()))
		first := crashtracker.Start()
		if first == nil || !strings.Contains(first.Error(), "crash") {
			t.Fatalf("Start with an invalid inherited descriptor = %v, want an error", first)
		}
		if later := crashtracker.Start(crashtracker.WithEnabled(false)); later != first {
			t.Fatalf("later Start = %v, want the first error %v", later, first)
		}
		return
	}
	for _, outcome := range []string{"disabled", "error"} {
		t.Run(outcome, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestStartIdempotent$")
			cmd.Env = append(filterE2EEnv(os.Environ()), outcomeEnv+"="+outcome, "GORACE=atexit_sleep_ms=0")
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := startVictim(t, cmd); err != nil {
				t.Fatal(err)
			}
			if err := waitForVictim(t, cmd, 5*time.Second); err != nil {
				t.Fatalf("Start helper: %v: %s", err, &output)
			}
		})
	}
}
