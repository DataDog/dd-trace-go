// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package orchestrionfixture

import (
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tinylib/msgp/msgp"
	"golang.org/x/mod/modfile"

	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

//go:embed testdata/client_test.go.tmpl
var clientSource []byte

func TestCIVisibilityOrchestrionEntryPoints(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs external Orchestrion clients")
	}
	if os.Getenv("GO_CMD") == "gotip" {
		t.Skip("requires an Orchestrion release supporting Go tip")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	orchestrionVersion, err := os.ReadFile(filepath.Join(root, ".orchestrion-version"))
	if err != nil {
		t.Fatal(err)
	}
	baseEnv := clientEnv()
	toolDir := t.TempDir()
	writeClient(t, toolDir, root, strings.TrimSpace(string(orchestrionVersion)), "civisibility")
	orchestrion := filepath.Join(toolDir, "orchestrion")
	if runtime.GOOS == "windows" {
		orchestrion += ".exe"
	}
	runCommand(t, toolDir, baseEnv, 5*time.Minute, "go", "build", "-mod=mod", "-o", orchestrion, "github.com/DataDog/orchestrion")

	// Each Orchestrion configuration recompiles the standard library. Avoid
	// competing cold builds while allowing the runtime scenarios to overlap.
	var buildMu sync.Mutex
	for _, entry := range []string{"civisibility", "orchestrion", "all", "plain"} {
		t.Run(entry, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeClient(t, dir, root, strings.TrimSpace(string(orchestrionVersion)), entry)
			toolFile := filepath.Join(dir, "orchestrion.tool.go")
			selectedIntegrations, err := os.ReadFile(toolFile)
			if err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(dir, "client.test")
			if runtime.GOOS == "windows" {
				binary += ".exe"
			}
			args := []string{"test", "-mod=mod", "-c", "-o", binary}
			func() {
				buildMu.Lock()
				defer buildMu.Unlock()
				if entry == "plain" {
					runCommand(t, dir, baseEnv, 10*time.Minute, "go", args...)
				} else {
					runCommand(t, dir, baseEnv, 10*time.Minute, orchestrion, append([]string{"go"}, args...)...)
				}
			}()
			actualIntegrations, err := os.ReadFile(toolFile)
			if err != nil || !bytes.Equal(actualIntegrations, selectedIntegrations) {
				t.Fatalf("build changed selected integrations (read error: %v):\n%s", err, actualIntegrations)
			}

			type scenario struct {
				name   string
				value  *string
				mode   string
				events int
			}
			tests := []scenario{
				{name: "unset"},
				{name: "false", value: new("false"), mode: "false"},
				{name: "invalid", value: new("invalid"), mode: "invalid"},
				{name: "empty", value: new(""), mode: ""},
			}
			if entry == "civisibility" {
				tests[0].mode, tests[0].events = "false", 1
			}
			if entry != "plain" {
				tests = append(tests,
					scenario{"true", new("true"), "1", 2},
					scenario{"parent", new("parent"), "false", 1},
				)
			} else {
				tests = append(tests,
					scenario{"true", new("true"), "true", 0},
					scenario{"parent", new("parent"), "parent", 0},
				)
			}
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					server, events := startIntake(t)
					env := append(append([]string{}, baseEnv...), intakeEnv(server.URL)...)
					if test.value != nil {
						env = append(env, "DD_CIVISIBILITY_ENABLED="+*test.value)
					}
					output := runCommand(t, dir, env, time.Minute, binary, "-test.run=^TestProbe$", "-test.v", "-test.timeout=30s")
					if strings.Count(output, "CI_MODE="+strconv.Quote(test.mode)) != 2 {
						t.Fatalf("parent/child mode mismatch; want %q twice:\n%s", test.mode, output)
					}
					if got := len(events()); got != test.events {
						t.Fatalf("reported %d tests, want %d:\n%s", got, test.events, output)
					}
				})
			}
			if entry == "civisibility" && (runtime.GOOS == "darwin" || runtime.GOOS == "linux" || runtime.GOOS == "windows") {
				t.Run("process retry", func(t *testing.T) {
					type retryScenario struct{ key, value, mode string }
					tests := []retryScenario{{mode: "false"}}
					if runtime.GOOS != "windows" {
						tests = append(tests,
							retryScenario{"dd_civisibility_enabled", "false", "false"},
							retryScenario{"Dd_CiVisibility_Enabled", "false", "false"},
						)
					}
					for _, key := range []string{"dd_civisibility_enabled", "Dd_CiVisibility_Enabled"} {
						mode := "false"
						if runtime.GOOS == "windows" {
							mode = "true"
						}
						tests = append(tests, retryScenario{key, "true", mode})
					}
					for _, test := range tests {
						name := test.key
						if name == "" {
							name = "unset"
						} else {
							name += "=" + test.value
						}
						t.Run(name, func(t *testing.T) {
							retryDir := t.TempDir()
							server, events := startIntake(t)
							env := append(append([]string{}, baseEnv...), intakeEnv(server.URL)...)
							env = append(env, "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process")
							if test.key != "" {
								env = append(env, test.key+"="+test.value)
							}
							output := runCommand(t, retryDir, env, time.Minute, binary, "-test.run=^TestRetry$", "-test.v", "-test.timeout=30s")
							mode, err := os.ReadFile(filepath.Join(retryDir, "retry-mode"))
							if err != nil || string(mode) != test.mode {
								t.Fatalf("retry child mode = %q, read error = %v; want %s:\n%s", mode, err, test.mode, output)
							}
							got := events()
							var initialFailures, passingRetries int
							for _, event := range got {
								meta := event.Content.Meta
								if meta["test.status"] == "fail" && meta["test.retry.execution_mode"] == "" {
									initialFailures++
								}
								if meta["test.status"] == "pass" && meta["test.retry.execution_mode"] == "process" && meta["test.is_retry"] == "true" {
									passingRetries++
								}
							}
							if len(got) != 2 || initialFailures != 1 || passingRetries != 1 {
								t.Fatalf("expected a failed parent attempt and passing process retry, got %+v:\n%s", got, output)
							}
						})
					}
				})
			}
		})
	}
}

func writeClient(t *testing.T, dir, root, orchestrionVersion, entry string) {
	t.Helper()
	var mod strings.Builder
	fmt.Fprintf(&mod, "module example.com/civisibility-client\n\ngo 1.26.0\n\nrequire (\n github.com/DataDog/dd-trace-go/v2 %s\n github.com/DataDog/orchestrion %s\n)\n", version.Tag, orchestrionVersion)
	if entry == "all" {
		fmt.Fprintf(&mod, "require github.com/DataDog/dd-trace-go/orchestrion/all/v2 %s\n", version.Tag)
	}
	workData, err := os.ReadFile(filepath.Join(root, "go.work"))
	if err != nil {
		t.Fatal(err)
	}
	work, err := modfile.ParseWork("go.work", workData, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, use := range work.Use {
		path := filepath.Join(root, use.Path)
		data, err := os.ReadFile(filepath.Join(path, "go.mod"))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&mod, "replace %s => %s\n", modfile.ModulePath(data), strconv.Quote(path))
	}
	for name, data := range map[string][]byte{"go.mod": []byte(mod.String()), "client_test.go": clientSource} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path := "github.com/DataDog/dd-trace-go/v2/" + entry
	if entry == "all" {
		path = "github.com/DataDog/dd-trace-go/orchestrion/all/v2"
	}
	tool := "//go:build tools\n\npackage tools\n\nimport (\n _ \"github.com/DataDog/orchestrion\"\n _ " + strconv.Quote(path) + " // integration\n)\n"
	if entry == "plain" {
		tool = "//go:build tools\n\npackage tools\n\nimport _ \"github.com/DataDog/dd-trace-go/v2/civisibility\"\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "orchestrion.tool.go"), []byte(tool), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestClientEnv(t *testing.T) {
	for _, test := range []struct{ key, want string }{
		{"DD_CIVISIBILITY_ENABLED", ""},
		{"dd_civisibility_enabled", ""},
		{"Dd_CiVisibility_Enabled", ""},
		{"OTEL_SERVICE_NAME", ""},
		{"otel_service_name", ""},
		{"Otel_Service_Name", ""},
		{"GOFLAGS", "GOFLAGS=-mod=mod"},
		{"goflags", "GOFLAGS=-mod=mod"},
		{"GoFlags", "GOFLAGS=-mod=mod"},
		{"GOWORK", "GOWORK=off"},
		{"gowork", "GOWORK=off"},
		{"GoWork", "GOWORK=off"},
		{"CivizFixture_Keep", "CivizFixture_Keep=inherited=value"},
	} {
		t.Run(test.key, func(t *testing.T) {
			t.Setenv(test.key, "inherited=value")
			var got []string
			for _, entry := range clientEnv() {
				key, _, _ := strings.Cut(entry, "=")
				if strings.EqualFold(key, test.key) {
					got = append(got, entry)
				}
			}
			if actual := strings.Join(got, "\n"); actual != test.want {
				t.Fatalf("clientEnv entries for %q = %q, want %q", test.key, actual, test.want)
			}
		})
	}
}

func clientEnv() []string {
	var result []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToUpper(key)
		if strings.HasPrefix(key, "DD_") || strings.HasPrefix(key, "OTEL_") || key == "GOFLAGS" || key == "GOWORK" {
			continue
		}
		result = append(result, entry)
	}
	// Resolve build dependencies without tidying every integration's transitive tests.
	return append(result, "GOWORK=off", "GOFLAGS=-mod=mod")
}

func intakeEnv(url string) []string {
	return []string{
		"DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL=" + url,
		"DD_API_KEY=fixture", "DD_CIVISIBILITY_GIT_UPLOAD_ENABLED=false",
		"DD_GIT_REPOSITORY_URL=https://example.com/civisibility-client.git",
		"DD_GIT_COMMIT_SHA=1234567890abcdef1234567890abcdef12345678",
		"DD_INSTRUMENTATION_TELEMETRY_ENABLED=false", "DD_APPSEC_ENABLED=false",
		"DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=1",
		"DD_CIVISIBILITY_TOTAL_FLAKY_RETRY_COUNT=1", "DD_SERVICE=civisibility-entry-fixture",
	}
}

func runCommand(t *testing.T, dir string, env []string, timeout time.Duration, name string, args ...string) string {
	t.Helper()
	if deadline, ok := t.Deadline(); ok {
		// Reserve time for tree cancellation and pipe cleanup before the test
		// process exits at its global timeout.
		timeout = min(timeout, time.Until(deadline)-15*time.Second)
	}
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	cmd := commandContext(ctx, name, args...)
	cmd.Dir, cmd.Env = dir, env
	started := time.Now()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed after %s: %v (context: %v)\n%s", name, args, time.Since(started), err, ctx.Err(), output)
	}
	return string(output)
}

type testEvent struct {
	Type    string `json:"type"`
	Content struct {
		Meta map[string]string `json:"meta"`
	} `json:"content"`
}

func startIntake(t *testing.T) (*httptest.Server, func() []testEvent) {
	t.Helper()
	var mu sync.Mutex
	var events []testEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.URL.Path == "/api/v2/citestcycle" {
			var reader io.Reader = r.Body
			if r.Header.Get("Content-Encoding") == "gzip" {
				gz, err := gzip.NewReader(r.Body)
				if err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				defer gz.Close()
				reader = gz
			}
			var data bytes.Buffer
			if _, err := msgp.CopyToJSON(&data, reader); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			var payload struct {
				Events []testEvent `json:"events"`
			}
			if err := json.Unmarshal(data.Bytes(), &payload); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			for _, event := range payload.Events {
				if event.Type == "test" {
					events = append(events, event)
				}
			}
			mu.Unlock()
		} else {
			_, _ = io.Copy(io.Discard, r.Body)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v2/libraries/tests/services/setting" {
			_, _ = io.WriteString(w, `{"data":{"id":"settings","type":"ci_app_libraries_tests_settings","attributes":{"flaky_test_retries_enabled":true}}}`)
		} else {
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(server.Close)
	return server, func() []testEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]testEvent{}, events...)
	}
}
