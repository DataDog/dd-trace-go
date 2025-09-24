// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.
package telemetrytest

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type contribPkg struct {
	ImportPath string
	Name       string
	Imports    []string
	Dir        string
}

var InstrumentationImport = "github.com/DataDog/dd-trace-go/v2/instrumentation"

func (p *contribPkg) hasInstrumentationImport() bool {
	for _, imp := range p.Imports {
		if imp == InstrumentationImport {
			return true
		}
	}
	return false
}

// TestTelemetryEnabled verifies that the expected contrib packages leverage instrumentation telemetry
func TestTelemetryEnabled(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "contrib"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(root); err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(path string, _ fs.DirEntry, _ error) error {
		if filepath.Base(path) != "go.mod" {
			return nil
		}
		if strings.Contains(path, "datadog-lambda-go/test/integration_tests/") {
			return nil
		}
		rErr := testTelemetryEnabled(t, filepath.Dir(path))
		if rErr != nil {
			return fmt.Errorf("path: %s, err: %w", path, rErr)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func testTelemetryEnabled(t *testing.T, contribPath string) error {
	t.Helper()
	t.Log(contribPath)
	pwd, err := os.Getwd()
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Chdir(pwd)
	}()
	if err = os.Chdir(contribPath); err != nil {
		return err
	}
	body, err := exec.Command("go", "list", "-json", "./...").Output()
	if err != nil {
		t.Log(string(body))
		return err
	}
	var packages []contribPkg
	stream := json.NewDecoder(strings.NewReader(string(body)))
	for stream.More() {
		var out contribPkg
		err := stream.Decode(&out)
		if err != nil {
			return err
		}
		packages = append(packages, out)
	}
	for _, pkg := range packages {
		if strings.Contains(pkg.ImportPath, "/test") || strings.Contains(pkg.ImportPath, "/internal") {
			continue
		}
		// Skip AWS SDK v2 subpackages
		if strings.Contains(pkg.ImportPath, "aws-sdk-go-v2/") && !strings.HasSuffix(pkg.ImportPath, "/aws") {
			continue
		}
		// Skip AWS SDK v2 subpackages
		if strings.Contains(pkg.ImportPath, "datadog-lambda-go/") {
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
