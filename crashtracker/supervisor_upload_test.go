// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestSupervisorUploadDeadline(t *testing.T) {
	received := make(chan time.Time, 3)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		received <- time.Now()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(filterE2EEnv(os.Environ()), e2eRoleEnv+"=panic",
		"DD_CRASHTRACKING_ENABLED=true", "DD_TRACE_AGENT_URL="+srv.URL, "GORACE=atexit_sleep_ms=0")
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	cmd.Stdout, cmd.Stderr = null, null
	started := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = waitForVictim(t, cmd, 15*time.Second)
	elapsed := time.Since(started)
	if cmd.ProcessState.ExitCode() != 2 {
		t.Fatalf("panic exit code = %d, want 2", cmd.ProcessState.ExitCode())
	}
	select {
	case requested := <-received:
		uploadElapsed := time.Since(requested)
		if uploadElapsed < 9*time.Second || elapsed > 12*time.Second {
			t.Fatalf("upload wait = %s, process lifetime = %s, want 10 seconds plus startup margin", uploadElapsed, elapsed)
		}
		t.Logf("supervisor exited after %s with an unresponsive intake (upload wait %s)", elapsed, uploadElapsed)
	default:
		t.Fatal("intake never received an upload attempt")
	}
}
