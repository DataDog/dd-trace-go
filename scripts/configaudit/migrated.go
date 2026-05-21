// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"
)

// providerGetterPrefixes are the method names on *provider.Provider that read
// a DD_* config value. Keep in sync with internal/config/provider/provider.go.
var providerGetterPrefixes = []string{
	"GetString", "GetStringWithValidator",
	"GetBool",
	"GetInt", "GetIntWithValidator",
	"GetFloat", "GetFloatWithValidator", "GetFloatWithValidatorOrigin",
	"GetDuration",
}

func isProviderGetter(name string) bool {
	for _, p := range providerGetterPrefixes {
		if name == p {
			return true
		}
	}
	return false
}

// loadMigrated walks the loadConfig function inside the package at pkgDir and
// returns the set of DD_* keys passed as the first argument to any provider
// getter call.
func loadMigrated(pkgDir string) (map[string]struct{}, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports |
			packages.NeedDeps,
		Dir: pkgDir,
	}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", pkgDir, err)
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no packages loaded from %s", pkgDir)
	}
	if errs := packageErrors(pkgs); len(errs) > 0 {
		return nil, fmt.Errorf("type errors in %s: %v", pkgDir, errs)
	}

	out := make(map[string]struct{})
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				fn, ok := n.(*ast.FuncDecl)
				if !ok || fn.Name.Name != "loadConfig" {
					return true
				}
				ast.Inspect(fn.Body, func(inner ast.Node) bool {
					call, ok := inner.(*ast.CallExpr)
					if !ok {
						return true
					}
					if len(call.Args) == 0 {
						return true
					}
					// Match provider getters (p.GetString, p.GetBool, ...) by
					// selector name, and additionally pick up any call whose
					// first argument resolves to a DD_*-prefixed string
					// constant (e.g. env.Get("DD_API_KEY"),
					// env.Lookup("DD_TRACE_SOURCE_HOSTNAME")). A config is
					// considered migrated if it is read inside loadConfig.
					providerCall := false
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && isProviderGetter(sel.Sel.Name) {
						providerCall = true
					}
					key, resolved := resolveStringArg(pkg.TypesInfo, call.Args[0])
					if !resolved {
						return true
					}
					if providerCall || strings.HasPrefix(key, "DD_") {
						out[key] = struct{}{}
					}
					return true
				})
				return false
			})
		}
	}
	return out, nil
}

// resolveStringArg returns the string value of expr if it is a constant string
// (literal or named constant), and the second return is true on success.
func resolveStringArg(info *types.Info, expr ast.Expr) (string, bool) {
	tv, ok := info.Types[expr]
	if !ok || tv.Value == nil {
		return "", false
	}
	if tv.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}

func packageErrors(pkgs []*packages.Package) []error {
	var errs []error
	for _, pkg := range pkgs {
		for _, e := range pkg.Errors {
			errs = append(errs, e)
		}
	}
	if len(errs) > 5 {
		errs = errs[:5]
	}
	return errs
}
