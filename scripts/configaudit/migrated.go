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
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/types/typeutil"
)

// providerGetters are the method names on *provider.Provider that read
// a DD_* config value. Keep in sync with internal/config/provider/provider.go.
var providerGetters = []string{
	"GetString", "GetStringWithValidator", "GetStringWithOrigin",
	"GetBool", "GetBoolWithOrigin",
	"GetInt", "GetIntWithValidator",
	"GetFloat", "GetFloatWithValidator", "GetFloatWithValidatorOrigin",
	"GetDuration",
	"GetMap",
}

// loadMigrated finds configuration keys read by loadConfig and its
// statically resolved package-local calls.
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
		collectMigratedReads(pkg, out)
	}
	return out, nil
}

func collectMigratedReads(pkg *packages.Package, out map[string]struct{}) {
	bodies := make(map[*types.Func]*ast.BlockStmt)
	var loadConfig *types.Func
	for _, file := range pkg.Syntax {
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			symbol, ok := pkg.TypesInfo.Defs[fn.Name].(*types.Func)
			if !ok {
				continue
			}
			bodies[symbol] = fn.Body
			if fn.Recv == nil && fn.Name.Name == "loadConfig" {
				loadConfig = symbol
			}
		}
	}

	var visit func(*types.Func)
	visit = func(symbol *types.Func) {
		body := bodies[symbol]
		if body == nil {
			return
		}
		delete(bodies, symbol)
		ast.Inspect(body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			visit(typeutil.StaticCallee(pkg.TypesInfo, call))
			if len(call.Args) == 0 {
				return true
			}
			key, resolved := resolveStringArg(pkg.TypesInfo, call.Args[0])
			if !resolved {
				return true
			}
			sel, isSelector := call.Fun.(*ast.SelectorExpr)
			// Legacy startup readers also pass DD_* keys to wrappers outside the provider.
			if strings.HasPrefix(key, "DD_") || isSelector && slices.Contains(providerGetters, sel.Sel.Name) {
				out[key] = struct{}{}
			}
			return true
		})
	}
	visit(loadConfig)
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
