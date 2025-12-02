// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package instrumentation

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
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
		if filepath.Base(path) != "go.mod" || strings.Contains(path, fmt.Sprintf("%cinternal", os.PathSeparator)) ||
			strings.Contains(path, fmt.Sprintf("%ctest%c", os.PathSeparator, os.PathSeparator)) {
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
	packages, err := parsePackages(contribPath)
	if err != nil {
		return fmt.Errorf("unable to get package info: %w", err)
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

func parsePackages(root string) ([]contribPkg, error) {
	var packages []contribPkg
	packageMap := make(map[string]*contribPkg)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil
		}

		dir := filepath.Dir(path)
		relPath, err := filepath.Rel(root, dir)
		if err != nil {
			return err
		}

		importPath := filepath.Join(getModulePath(root), relPath)
		importPath = filepath.ToSlash(importPath)

		pkg, exists := packageMap[importPath]
		if !exists {
			pkg = &contribPkg{
				ImportPath: importPath,
				Imports:    make([]string, 0),
			}
			packageMap[importPath] = pkg
		}

		for _, imp := range node.Imports {
			importStr := strings.Trim(imp.Path.Value, `"`)
			found := false
			for _, existing := range pkg.Imports {
				if existing == importStr {
					found = true
					break
				}
			}
			if !found {
				pkg.Imports = append(pkg.Imports, importStr)
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	for _, pkg := range packageMap {
		packages = append(packages, *pkg)
	}

	return packages, nil
}

func getModulePath(root string) string {
	goModPath := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return ""
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module"))
		}
	}
	return ""
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
	ImportPath string
	Imports    []string
}
