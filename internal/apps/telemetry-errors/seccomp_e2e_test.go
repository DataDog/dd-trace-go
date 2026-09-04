// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seccompProfile blocks memfd_create, matching the README's "Running the
// memfd/OTel-process-context trigger" recipe: it forces storeConfig's first
// site (CreateMemfd) to fail deterministically on any Linux kernel.
const seccompProfile = `{
  "defaultAction": "SCMP_ACT_ALLOW",
  "architectures": ["SCMP_ARCH_X86_64", "SCMP_ARCH_AARCH64"],
  "syscalls": [{"names": ["memfd_create"], "action": "SCMP_ACT_ERRNO"}]
}`

// wireLogMessage mirrors the JSON fields of
// internal/telemetry/internal/transport.LogMessage that this test needs.
// internal/apps is a separate Go module (see its own go.mod) whose import
// path doesn't share dd-trace-go/v2's prefix, so it structurally cannot
// import that internal package — decoding by field name into a local type
// works identically, since encoding/json matches by tag, not type identity.
type wireLogMessage struct {
	Message    string `json:"message"`
	Level      string `json:"level"`
	Count      uint32 `json:"count"`
	StackTrace string `json:"stack_trace"`
}

type wireBody struct {
	RequestType string          `json:"request_type"`
	Payload     json.RawMessage `json:"payload"`
}

type wireLogsPayload struct {
	Logs []wireLogMessage `json:"logs"`
}

// TestSeccompMemfdBlocked_ReportsWellFormedErrors drives the harness binary
// inside a Docker container whose seccomp profile blocks memfd_create,
// forcing storeConfig's two sites (ddtrace/tracer/tracer.go) to fail. It
// captures what would be sent to Error Tracking via the payload-files-dump
// mechanism (DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES) — no real agent or
// backend needed. This automates the README's "Running the memfd/OTel-
// process-context trigger" recipe end to end.
func TestSeccompMemfdBlocked_ReportsWellFormedErrors(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("storeConfig's memfd/OTel sites are no-ops outside Linux")
	}
	if os.Getenv("INTEGRATION") == "" {
		t.Skip("set INTEGRATION=true to run this Docker-based test")
	}
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker not found on PATH")
	}
	if err := exec.Command(dockerPath, "info").Run(); err != nil {
		t.Skip("docker daemon not reachable")
	}

	tempDir := t.TempDir()
	binaryPath := filepath.Join(tempDir, "telemetry-errors-linux")
	outDir := filepath.Join(tempDir, "out")
	require.NoError(t, os.MkdirAll(outDir, 0o755))

	seccompPath := filepath.Join(tempDir, "no-memfd-seccomp.json")
	require.NoError(t, os.WriteFile(seccompPath, []byte(seccompProfile), 0o644))

	// Always cross-compile for Linux regardless of host OS — the target is
	// always a Linux container, matching the runner's own architecture so
	// Docker runs it natively (no DOCKER_DEFAULT_PLATFORM override needed).
	build := exec.Command("go", "build", "-o", binaryPath, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building harness binary: %s\n%s", err.Error(), out)
	}

	containerName := fmt.Sprintf("telemetry-errors-seccomp-test-%d", time.Now().UnixNano())
	run := exec.Command(dockerPath, "run", "--rm",
		"--name", containerName,
		"--security-opt", "seccomp="+seccompPath,
		"-e", "DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES=true",
		"-e", "TEST_UNDECLARED_OUTPUTS_DIR=/out",
		"-e", "DD_TELEMETRY_HEARTBEAT_INTERVAL=2",
		"-v", binaryPath+":/telemetry-errors-linux:ro",
		"-v", outDir+":/out",
		"--entrypoint", "/telemetry-errors-linux",
		"alpine:latest", "-http", "127.0.0.1:8080",
	)
	var stderr bytes.Buffer
	run.Stderr = &stderr
	require.NoError(t, run.Start(), "starting container")
	t.Cleanup(func() {
		_ = exec.Command(dockerPath, "stop", containerName).Run()
		_ = run.Wait()
	})

	// storeConfig fires unconditionally at tracer startup, and the flush
	// ticker (sped up via DD_TELEMETRY_HEARTBEAT_INTERVAL) drains it shortly
	// after — but the two sites can land in different flushes (see the
	// README's "Known gaps"), so wait out the full window before reading
	// whatever payload files exist, rather than stopping at the first one.
	payloadsDir := filepath.Join(outDir, "payloads", "telemetry")
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if entries, _ := os.ReadDir(payloadsDir); len(entries) > 0 {
			time.Sleep(3 * time.Second) // give a same-run second flush a chance to land too
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	entries, _ := os.ReadDir(payloadsDir)
	require.NotEmpty(t, entries, "no telemetry payload files were dumped within the timeout; container stderr: %s", stderr.String())

	logs := readAllLogMessages(t, payloadsDir, entries)

	memfdMsg := findLogMessage(logs, "failed to store the configuration")
	require.NotNil(t, memfdMsg, "memfd_create being blocked by seccomp must deterministically produce this message")
	assert.Contains(t, memfdMsg.Message, "error.error_type=")
	assert.Equal(t, "ERROR", memfdMsg.Level)
	assert.Contains(t, memfdMsg.StackTrace, "storeConfig")

	// Best-effort only: this second site additionally needs the mmap/prctl
	// fallback to also fail, which depends on the runner's own kernel — a
	// modern CI kernel may well support PR_SET_VMA and make that fallback
	// succeed, in which case this message legitimately never appears. Do
	// not turn this into a hard assertion without first resolving that gap.
	if otelMsg := findLogMessage(logs, "failed to publish the OTEL process context"); otelMsg != nil {
		assert.Contains(t, otelMsg.Message, "error.error_type=")
		assert.Equal(t, "ERROR", otelMsg.Level)
		assert.Contains(t, otelMsg.StackTrace, "storeConfig")
	} else {
		t.Log("OTEL process-context message did not appear — expected on kernels where the mmap/prctl fallback succeeds; see README's Known gaps")
	}
}

// readAllLogMessages decodes every dumped payload file's transport.Body
// (see internal/telemetry/internal/writer.go's encodePayloadForBazelFile,
// which encodes the same struct sent over the wire) into wireLogMessage,
// flattening across however many files got written.
func readAllLogMessages(t *testing.T, dir string, entries []os.DirEntry) []wireLogMessage {
	t.Helper()

	var logs []wireLogMessage
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		require.NoError(t, err)

		var body wireBody
		if err := json.Unmarshal(data, &body); err != nil {
			t.Logf("skipping %s: not a decodable telemetry body: %s", entry.Name(), err.Error())
			continue
		}

		switch body.RequestType {
		case "logs":
			var payload wireLogsPayload
			require.NoError(t, json.Unmarshal(body.Payload, &payload))
			logs = append(logs, payload.Logs...)
		case "message-batch":
			var batch []wireBody
			require.NoError(t, json.Unmarshal(body.Payload, &batch))
			for _, msg := range batch {
				if msg.RequestType != "logs" {
					continue
				}
				var payload wireLogsPayload
				require.NoError(t, json.Unmarshal(msg.Payload, &payload))
				logs = append(logs, payload.Logs...)
			}
		}
	}
	return logs
}

func findLogMessage(logs []wireLogMessage, messagePrefix string) *wireLogMessage {
	for i := range logs {
		if strings.HasPrefix(logs[i].Message, messagePrefix) {
			return &logs[i]
		}
	}
	return nil
}
