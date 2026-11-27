// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/crashtracker"
)

// A terminal on stderr must reach the application unchanged. The application
// then keeps its terminal behavior, and the supervisor does not tee.
func TestSupervisorKeepsTerminalStderr(t *testing.T) {
	_, slave := crashtracker.OpenTestPTY(t)
	resultPath := filepath.Join(t.TempDir(), "mode")
	cmd := startStderrVictim(t, "stderr-mode", "http://127.0.0.1:1", slave, e2eGoFileEnv+"="+resultPath)
	_ = waitForVictim(t, cmd, 30*time.Second)
	mode, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mode), "c") {
		t.Fatalf("application stderr mode = %q, want a character device", mode)
	}
}
