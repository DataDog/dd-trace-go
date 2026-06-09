// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"
)

// CallSite records where a DD_* env var was read.
type CallSite struct {
	File    string
	Line    int
	Func    string // fully-qualified or fixture-local function name
	Package string // import path of the containing package
}

// recognizers describes how to identify env-reading function calls.
//
//   - ByPath: map[importPath]map[funcName]bool — used in the real codebase
//   - ByName: map[funcName]bool — used in unit-test fixtures where the helpers
//     have no stable import path
type recognizers struct {
	ByPath map[string]map[string]bool
	ByName map[string]bool
}

func defaultRecognizers() recognizers {
	return recognizers{
		ByPath: map[string]map[string]bool{
			"github.com/DataDog/dd-trace-go/v2/internal/env": {
				"Get":    true,
				"Lookup": true,
			},
			"github.com/DataDog/dd-trace-go/v2/internal": {
				"BoolEnv":             true,
				"BoolEnvNoDefault":    true,
				"IntEnv":              true,
				"FloatEnv":            true,
				"DurationEnv":         true,
				"DurationEnvWithUnit": true,
			},
			"github.com/DataDog/dd-trace-go/v2/internal/stableconfig": {
				"Bool":   true,
				"String": true,
				"Int":    true,
				"Float":  true,
			},
		},
	}
}

func defaultExcludes(root string) []string {
	// Patterns are matched as a substring of the file path returned by go/packages.
	return []string{
		"/internal/config/",
		"/internal/env/",
		"/scripts/",
		"_test.go",
	}
}

func excluded(path string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(path, p) {
			return true
		}
	}
	return false
}

// scan walks every package under root and returns the DD_* key -> call sites map.
func scan(root string, r recognizers, exclude []string) (map[string][]CallSite, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports |
			packages.NeedDeps | packages.NeedCompiledGoFiles,
		Dir:   root,
		Tests: false,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("packages.Load: %w", err)
	}
	out := make(map[string][]CallSite)
	for _, pkg := range pkgs {
		for i, file := range pkg.Syntax {
			filename := pkg.CompiledGoFiles[i]
			if excluded(filename, exclude) {
				continue
			}
			suppressed := suppressedLines(file, pkg)
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				funcID, recognized := callIdentity(pkg, call, r)
				if !recognized {
					return true
				}
				key, ok := resolveStringArg(pkg.TypesInfo, call.Args[0])
				if !ok {
					return true
				}
				if !strings.HasPrefix(key, "DD_") && !strings.HasPrefix(key, "DD-") && !strings.HasPrefix(key, "OTEL_") {
					return true
				}
				pos := pkg.Fset.Position(call.Pos())
				if suppressed[pos.Line] {
					return true
				}
				out[key] = append(out[key], CallSite{
					File:    pos.Filename,
					Line:    pos.Line,
					Func:    funcID,
					Package: pkg.PkgPath,
				})
				return true
			})
		}
	}
	return out, nil
}

// suppressedLines returns the set of 1-based line numbers in file that carry
// a //nolint:configaudit annotation. Calls on those lines are intentionally
// not migrated and are excluded from the audit output.
func suppressedLines(file *ast.File, pkg *packages.Package) map[int]bool {
	out := map[int]bool{}
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			if strings.Contains(c.Text, "nolint:configaudit") {
				out[pkg.Fset.Position(c.Pos()).Line] = true
			}
		}
	}
	return out
}

// callIdentity decides whether the call matches one of our recognizers, and
// returns a printable function identity ("pkg.Func" or just "Func" for fixtures).
func callIdentity(pkg *packages.Package, call *ast.CallExpr, r recognizers) (string, bool) {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		// Same-package call. Try ByName recognizer first.
		if r.ByName != nil && r.ByName[fn.Name] {
			return fn.Name, true
		}
		// Try ByPath using this package's path.
		if names, ok := r.ByPath[pkg.PkgPath]; ok && names[fn.Name] {
			return pkg.PkgPath + "." + fn.Name, true
		}
		return "", false
	case *ast.SelectorExpr:
		// pkgname.Func form. Resolve the imported package.
		obj, ok := pkg.TypesInfo.Uses[fn.Sel]
		if !ok {
			return "", false
		}
		// Only function calls (not method calls) are env helpers.
		fnObj, ok := obj.(*types.Func)
		if !ok {
			return "", false
		}
		impPkg := fnObj.Pkg()
		if impPkg == nil {
			return "", false
		}
		path := impPkg.Path()
		if names, ok := r.ByPath[path]; ok && names[fnObj.Name()] {
			return path + "." + fnObj.Name(), true
		}
		if r.ByName != nil && r.ByName[fnObj.Name()] {
			return fnObj.Name(), true
		}
		return "", false
	}
	return "", false
}
