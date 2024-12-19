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
	p := fmt.Sprintf("..%c..%c..%ccontrib", os.PathSeparator, os.PathSeparator, os.PathSeparator)
	root, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(root); err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if filepath.Base(path) != "go.mod" {
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
	separator := fmt.Sprintf("%c", os.PathSeparator)
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Chdir(pwd)
	}()
	if err = os.Chdir(contribPath); err != nil {
		return err
	}
	body, err := exec.Command("go", "list", "-json", "."+separator+"...").Output()
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
		if strings.Contains(pkg.ImportPath, separator+"test") || strings.Contains(pkg.ImportPath, separator+"internal") {
			continue
		}
		if !pkg.hasInstrumentationImport() {
			return fmt.Errorf(`package %q is expected use instrumentation telemetry. For more info see https://github.com/DataDog/dd-trace-go/blob/main/contrib/README.md#instrumentation-telemetry`, pkg.ImportPath)
		}
	}
	return nil
}
