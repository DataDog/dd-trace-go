// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package instrumentation

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

func TestIntegrationEnabled(t *testing.T) {
	root, err := filepath.Abs("../contrib")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(root); err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(path string, _ fs.DirEntry, _ error) error {
		if filepath.Base(path) != "go.mod" || strings.Contains(path, fmt.Sprintf("%cinternal", os.PathSeparator)) {
			return nil
		}
		rErr := testIntegrationEnabled(t, filepath.Dir(path))
		if rErr != nil {
			return fmt.Errorf("path: %s, err: %w", path, rErr)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func testIntegrationEnabled(t *testing.T, contribPath string) error {
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
		return fmt.Errorf("unable to get package info: %w", err)
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
		if strings.Contains(pkg.ImportPath, "/test") || strings.Contains(pkg.ImportPath, "/internal") || strings.Contains(pkg.ImportPath, "/cmd") {
			continue
		}
		if hasInstrumentationImport(pkg) {
			return nil
		}
	}
	return fmt.Errorf(`package %q is expected use instrumentation telemetry. For more info see https://github.com/DataDog/dd-trace-go/blob/main/contrib/README.md#instrumentation-telemetry`, contribPath)
}

func hasInstrumentationImport(p contribPkg) bool {
	for _, imp := range p.Imports {
		if imp == "github.com/DataDog/dd-trace-go/v2/instrumentation" {
			return true
		}
	}
	return false
}

type contribPkg struct {
	Dir        string
	Root       string
	ImportPath string
	Name       string
	Imports    []string
}
