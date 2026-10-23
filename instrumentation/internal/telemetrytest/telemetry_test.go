// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.
package telemetrytest

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type contribPkg struct {
	ImportPath string
	Name       string
	Imports    []string
	Dir        string
	Module     *contribModule
}

type contribModule struct {
	Path string
	Dir  string
}

var InstrumentationImport = "github.com/DataDog/dd-trace-go/v2/instrumentation"

func (p *contribPkg) hasInstrumentationImport() bool {
	return slices.Contains(p.Imports, InstrumentationImport)
}

// TestTelemetryEnabled verifies that the expected contrib packages leverage instrumentation telemetry
func TestTelemetryEnabled(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(root); err != nil {
		t.Fatal(err)
	}

	// One workspace-wide go list replaces one go list per contrib module: each
	// per-module invocation reloaded the whole workspace module graph, which
	// dominated this test's runtime.
	cmd := exec.Command("go", "list", "-json", "github.com/DataDog/dd-trace-go/contrib/...")
	cmd.Dir = root
	body, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var packages []contribPkg
	stream := json.NewDecoder(strings.NewReader(string(body)))
	for stream.More() {
		var out contribPkg
		if err := stream.Decode(&out); err != nil {
			t.Fatal(err)
		}
		packages = append(packages, out)
	}

	modules := make(map[string][]contribPkg)
	for _, pkg := range packages {
		if pkg.Module == nil || pkg.Module.Dir == "" {
			continue
		}
		modules[pkg.Module.Dir] = append(modules[pkg.Module.Dir], pkg)
	}
	for _, moduleDir := range slices.Sorted(maps.Keys(modules)) {
		if skipContribModule(moduleDir) {
			continue
		}
		if rErr := testTelemetryEnabled(t, moduleDir, modules[moduleDir]); rErr != nil {
			t.Fatalf("path: %s, err: %v", moduleDir, rErr)
		}
	}
}

// skipContribModule reports whether the module at moduleDir leaves the
// telemetry check below. Its rules mirror the module walk this test used
// before the single go list replaced it.
func skipContribModule(moduleDir string) bool {
	if strings.Contains(moduleDir, "integration_tests") ||
		strings.Contains(moduleDir, fmt.Sprintf("%ctest%c", os.PathSeparator, os.PathSeparator)) {
		return true
	}
	// otelc hook modules call into the contrib, which loads telemetry itself.
	return filepath.Base(moduleDir) == "otelc"
}

func testTelemetryEnabled(t *testing.T, contribPath string, packages []contribPkg) error {
	t.Helper()
	t.Log(contribPath)
	for _, pkg := range packages {
		if strings.Contains(pkg.ImportPath, "/test") || strings.Contains(pkg.ImportPath, "/internal") {
			continue
		}
		// Skip AWS SDK v2 subpackages
		if strings.Contains(pkg.ImportPath, "aws-sdk-go-v2/") && !strings.HasSuffix(pkg.ImportPath, "/aws") {
			continue
		}
		// Skip command subpackages
		if strings.Contains(pkg.ImportPath, "/cmd/") {
			continue
		}
		// Skip net/http subpackages
		if strings.Contains(pkg.ImportPath, "/net/http/v2") {
			continue
		}
		if !pkg.hasInstrumentationImport() {
			return fmt.Errorf(`package %q is expected use instrumentation telemetry. For more info see https://github.com/DataDog/dd-trace-go/blob/main/contrib/README.md#instrumentation-telemetry`, pkg.ImportPath)
		}
	}
	return nil
}
