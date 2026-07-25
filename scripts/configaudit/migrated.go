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

	"golang.org/x/tools/go/packages"
)

// loadMigrated validates the raw definitions and consumer bindings declared in
// pkgDir and returns the set of raw keys with at least one valid binding.
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
	registry, err := loadRegistry(pkgs)
	if err != nil {
		return nil, fmt.Errorf("invalid configuration registry in %s: %w", pkgDir, err)
	}
	migrated := make(map[string]struct{}, len(registry.rawKeys))
	for key := range registry.rawKeys {
		migrated[key] = struct{}{}
	}
	return migrated, nil
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

// resolveUintArg returns the unsigned integer value of expr if it is a
// constant integer (literal, named constant, or constant conversion).
func resolveUintArg(info *types.Info, expr ast.Expr) (uint64, bool) {
	tv, ok := info.Types[expr]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.Int {
		return 0, false
	}
	return constant.Uint64Val(tv.Value)
}

// resolveBoolArg returns the boolean value of expr if it is a constant boolean.
func resolveBoolArg(info *types.Info, expr ast.Expr) (bool, bool) {
	tv, ok := info.Types[expr]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.Bool {
		return false, false
	}
	return constant.BoolVal(tv.Value), true
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
