// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/orchestrion/runtime/built"
)

const idleVictimImportPath = "github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/crashtracker/idlevictim"

var (
	idleVictimPIDPattern   = regexp.MustCompile(`idlevictim: pid=(\d+)`)
	profilerStartedPattern = regexp.MustCompile(`Profiler configuration: .*"process_id:(\d+)"`)
)

// telemetryRequest is the part of an instrumentation telemetry request that
// the test reads.
type telemetryRequest struct {
	RequestType string `json:"request_type"`
	RuntimeID   string `json:"runtime_id"`
	Payload     struct {
		Products map[string]struct {
			Enabled bool `json:"enabled"`
		} `json:"products"`
	} `json:"payload"`
}

// TestCrashtrackerSupervisorDoesNotStartTracerOrProfiler builds a program with
// the crashtracker aspect, the tracer aspect, and the profiler aspect, then
// runs it against a fake agent. Orchestrion injects the tracer and profiler
// start calls into init functions of package main, and the supervisor runs
// them too unless the crashtracker aspect holds them back. The test counts
// the processes that start each product:
//
//   - The tracer reports one app-started telemetry request per process, with
//     the runtime ID of that process.
//   - The profiler logs its configuration once per process, with the PID of
//     that process.
//
// Exactly one process, the application, must do each.
func TestCrashtrackerSupervisorDoesNotStartTracerOrProfiler(t *testing.T) {
	if !built.WithOrchestrion {
		t.Skip("requires an orchestrion-built test binary; run via orchestrion go test")
	}

	binary := filepath.Join(t.TempDir(), "idlevictim")
	if runtime.GOOS == "windows" {
		binary += ".exe" // see TestCrashtrackerMainInjection
	}
	buildVictim(t, integrationModuleRoot(t), binary, idleVictimImportPath, true)

	var (
		mu       sync.Mutex
		started  = map[string]telemetryRequest{} // by runtime ID
		requests []string
	)
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer w.WriteHeader(http.StatusOK)
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		if !strings.HasSuffix(r.URL.Path, "/apmtelemetry") {
			return
		}
		var body io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				return
			}
			defer gz.Close()
			body = gz
		}
		var req telemetryRequest
		if err := json.NewDecoder(body).Decode(&req); err != nil {
			return
		}
		if req.RequestType == "app-started" {
			started[req.RuntimeID] = req
		}
	}))
	defer agent.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary) //nolint:gosec
	cmd.Env = append(filterInjectionEnv(os.Environ()),
		"DD_TRACE_AGENT_URL="+agent.URL,
		"DD_CRASHTRACKING_ENABLED=true",
		"DD_PROFILING_ENABLED=true",
		"DD_REMOTE_CONFIGURATION_ENABLED=false",
		"DD_SERVICE=crashtracker-supervisor-start-count",
	)
	prepareCrashtrackerCommand(cmd)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("run idlevictim: timeout\n%s", out)
	}
	if execErr, ok := errors.AsType[*exec.Error](err); ok {
		t.Fatalf("run idlevictim: %v", execErr)
	}
	if err != nil {
		t.Fatalf("run idlevictim: %v\n%s", err, out)
	}

	// The application is the only process that runs main's body.
	victimPIDs := idleVictimPIDPattern.FindAllStringSubmatch(string(out), -1)
	if len(victimPIDs) != 1 {
		t.Fatalf("got %d idlevictim PID lines, want 1 (the application)\n%s", len(victimPIDs), out)
	}
	applicationPID := victimPIDs[0][1]

	profilerPIDs := profilerStartedPattern.FindAllStringSubmatch(string(out), -1)
	if len(profilerPIDs) != 1 {
		t.Errorf("the profiler started in %d processes, want 1 (the application)\n%s", len(profilerPIDs), out)
	} else if profilerPIDs[0][1] != applicationPID {
		t.Errorf("the profiler started in process %s, want the application, process %s", profilerPIDs[0][1], applicationPID)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(started) != 1 {
		t.Errorf("the tracer started in %d processes, want 1 (the application); app-started runtime IDs: %v\nagent requests: %v", len(started), runtimeIDs(started), requests)
	}
	for id, req := range started {
		// The profiler may register its product after app-started, so only the tracer is checked here.
		if !req.Payload.Products["tracers"].Enabled {
			t.Errorf("app-started of runtime %s does not report the tracer as enabled: %+v", id, req.Payload.Products)
		}
	}
}

func runtimeIDs(started map[string]telemetryRequest) []string {
	ids := make([]string, 0, len(started))
	for id := range started {
		ids = append(ids, id)
	}
	return ids
}
