// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.
package telemetrytest

import (
	"errors"
	"fmt"
	"go/build"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
)

type contribPkg struct {
	ImportPath string
	Imports    []string
}

var InstrumentationImport = "github.com/DataDog/dd-trace-go/v2/instrumentation"

func (p *contribPkg) hasInstrumentationImport() bool {
	return slices.Contains(p.Imports, InstrumentationImport)
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
	err = filepath.WalkDir(root, func(path string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filepath.Base(path) != "go.mod" {
			return nil
		}
		if strings.Contains(path, "integration_tests") ||
			strings.Contains(path, fmt.Sprintf("%ctest%c", os.PathSeparator, os.PathSeparator)) {
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
	packages, err := parsePackages(contribPath)
	if err != nil {
		return err
	}
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

func parsePackages(root string) ([]contribPkg, error) {
	modulePath, err := getModulePath(root)
	if err != nil {
		return nil, err
	}

	var packages []contribPkg
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		if path != root {
			if ignoredPackageDir(entry.Name()) {
				return fs.SkipDir
			}
			_, statErr := os.Stat(filepath.Join(path, "go.mod"))
			switch {
			case statErr == nil:
				return fs.SkipDir
			case !errors.Is(statErr, fs.ErrNotExist):
				return statErr
			}
		}

		pkg, importErr := build.Default.ImportDir(path, 0)
		if importErr != nil {
			var noGoErr *build.NoGoError
			if errors.As(importErr, &noGoErr) {
				return nil
			}
			return fmt.Errorf("parse package in %s: %w", path, importErr)
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		importPath := modulePath
		if relPath != "." {
			importPath += "/" + filepath.ToSlash(relPath)
		}
		packages = append(packages, contribPkg{
			ImportPath: importPath,
			Imports:    pkg.Imports,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(packages, func(i, j int) bool {
		return packages[i].ImportPath < packages[j].ImportPath
	})
	return packages, nil
}

func ignoredPackageDir(name string) bool {
	return name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

func getModulePath(root string) (string, error) {
	goModPath := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return "", err
	}
	modulePath := modfile.ModulePath(data)
	if modulePath == "" {
		return "", fmt.Errorf("module path not found in %s", goModPath)
	}
	return modulePath, nil
}

func TestParsePackages(t *testing.T) {
	root := t.TempDir()
	writeFile := func(name, content string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeFile("go.mod", "module \"example.com/fixture\"\n\ngo 1.26.0\n")
	writeFile("fixture.go", "package fixture\n\nimport _ \""+InstrumentationImport+"\"\n")
	inactiveOS := "windows"
	if build.Default.GOOS == inactiveOS {
		inactiveOS = "linux"
	}
	writeFile("fixture_"+inactiveOS+".go", "package fixture\n\nimport _ \"example.com/inactive\"\n")
	writeFile("sub/sub.go", "package sub\n\nimport _ \""+InstrumentationImport+"\"\n")
	writeFile("nested/go.mod", "module example.com/nested\n")
	writeFile("nested/nested.go", "package nested\n")
	writeFile("testdata/testdata.go", "package testdata\n")
	writeFile("vendor/vendor.go", "package vendor\n")
	writeFile(".hidden/hidden.go", "package hidden\n")
	writeFile("_hidden/hidden.go", "package hidden\n")

	packages, err := parsePackages(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 2 {
		t.Fatalf("expected 2 packages, got %d: %#v", len(packages), packages)
	}
	if packages[0].ImportPath != "example.com/fixture" {
		t.Fatalf("unexpected root import path: %q", packages[0].ImportPath)
	}
	if !slices.Equal(packages[0].Imports, []string{InstrumentationImport}) {
		t.Fatalf("unexpected root imports: %q", packages[0].Imports)
	}
	if packages[1].ImportPath != "example.com/fixture/sub" {
		t.Fatalf("unexpected subpackage import path: %q", packages[1].ImportPath)
	}
}
