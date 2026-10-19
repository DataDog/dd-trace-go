// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"
)

// productionLogPackagePath is the import path of the root module's internal/log
// package, whose Error and Warn functions are the adoption surface of this
// audit.
const (
	productionLogPackagePath = "github.com/DataDog/dd-trace-go/v2/internal/log"
	reportingLogPackagePath  = "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"
)

const (
	levelError = "ERROR"
	levelWarn  = "WARN"
)

// Site is one internal/log Error or Warn call site.
type Site struct {
	File    string // path relative to the repository root, slash-separated
	Line    int
	Package string // import path relative to the module root
	Func    string // enclosing function, "(*Type).Method" for methods
	Level   string // "ERROR" or "WARN"
	Message string // constant format string, or "(non-constant)"
	Ignored bool   // carries an //errtrack:ignore directive

	// column disambiguates two distinct calls reported on the same line
	// (e.g. "log.Error(a); log.Warn(b)") from the same call reappearing
	// across platform loads. It is not part of the reported output.
	column int
	// endLine is the call's closing line, equal to Line for a single-line
	// call. It distinguishes a multi-line call's interior-comment
	// suppression rule from a same-line call's nearest-preceding-comment
	// rule in applyIgnoreDirectives. Not part of the reported output.
	endLine int
}

// scanOptions parameterizes scan. The log package path is configurable so unit
// tests can run against a fixture module that stubs internal/log; the
// production value is productionLogPackagePath.
type scanOptions struct {
	logPackagePath       string
	reportingPackagePath string
	exclude              []string
	platforms            []buildPlatform
}

type buildPlatform struct {
	goos   string
	goarch string
}

func defaultScanOptions() scanOptions {
	return scanOptions{
		logPackagePath:       productionLogPackagePath,
		reportingPackagePath: reportingLogPackagePath,
		exclude:              defaultExcludes(),
		// The repository currently has audited calls in Linux-portable files
		// and one Windows-only file. Scan both so CI reports the same complete
		// inventory instead of silently dropping the Windows sites.
		platforms: []buildPlatform{{goos: "linux", goarch: "amd64"}, {goos: "windows", goarch: "amd64"}},
	}
}

// defaultExcludes lists files that can never adopt the reporting API, or that
// are otherwise out of scope. Patterns are matched as a substring of the file
// path returned by go/packages.
//
// Everything else in the repository is out of scope by construction: scan
// loads a single module (packages.Load with GOWORK=off and pattern "./..." does
// not cross module boundaries), so the ~75 contrib/* integration modules and
// the workspace-sibling tooling modules are never scanned. They could not
// adopt this API anyway — Go's internal-visibility rule is import-path-based,
// and their module paths sit outside github.com/DataDog/dd-trace-go/v2/.
func defaultExcludes() []string {
	return []string{
		// The logger's own implementation. internal/telemetry/log imports
		// internal/log, so the reverse edge is a compile-time import cycle:
		// these call sites can never call the reporting API.
		"/internal/log/",
		// The reporting API's own implementation: LogAndReportError and
		// friends call internal/log.Error on the caller's behalf, and the
		// analyzer's testdata exists to test detection, not to report.
		// (The go tool also skips testdata directories; this is belt and
		// suspenders, like the _test.go entry below — Tests:false already
		// drops test files from the load.)
		"/internal/telemetry/log/",
		"_test.go",
		"/testdata/",
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

// scan walks every package of the module at root and returns every
// internal/log Error/Warn call site in scope, sorted by file path and line.
// It merges configured build platforms and deduplicates portable files that
// appear in more than one load.
func scan(root string, opts scanOptions) ([]Site, error) {
	if opts.logPackagePath == "" {
		return nil, errors.New("scan: empty log package path")
	}
	// go/packages reports absolute file paths even for a relative Dir, so
	// make the root absolute up front for the filepath.Rel below.
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	platforms := opts.platforms
	if len(platforms) == 0 {
		platforms = []buildPlatform{{}}
	}
	unique := make(map[string]Site)
	for _, platform := range platforms {
		sites, err := scanPlatform(absRoot, opts, platform)
		if err != nil {
			return nil, err
		}
		for _, site := range sites {
			unique[fmt.Sprintf("%s:%d:%d", site.File, site.Line, site.column)] = site
		}
	}
	sites := make([]Site, 0, len(unique))
	for _, site := range unique {
		sites = append(sites, site)
	}
	slices.SortStableFunc(sites, func(a, b Site) int {
		if c := strings.Compare(a.File, b.File); c != 0 {
			return c
		}
		if c := a.Line - b.Line; c != 0 {
			return c
		}
		return a.column - b.column
	})
	return sites, nil
}

// scanPlatform loads the module at root for one build platform and returns
// every audited call site in scope: the platform load, the fail-closed error
// collection, and the reporting-closure exclusion read as separate steps.
func scanPlatform(root string, opts scanOptions, platform buildPlatform) ([]Site, error) {
	pkgs, err := loadPlatform(root, platform)
	if err != nil {
		return nil, err
	}
	if errs := packageErrors(pkgs); len(errs) > 0 {
		return nil, fmt.Errorf("the scanned module did not load cleanly for %s/%s; the audit would be incomplete:\n%v", platform.goos, platform.goarch, errs)
	}
	excludedPackages, err := reportingDependencies(pkgs, opts.reportingPackagePath)
	if err != nil {
		return nil, err
	}
	return scanPackages(pkgs, excludedPackages, root, opts)
}

// loadPlatform loads every package of the module at root for one build
// platform. GOWORK=off keeps the scan on the module at root even when a Go
// workspace is active. CGO_ENABLED=0 makes cross-platform type checking
// independent of the host C toolchain.
func loadPlatform(root string, platform buildPlatform) ([]*packages.Package, error) {
	env := append([]string{}, os.Environ()...)
	env = append(env, "GOWORK=off")
	if platform.goos != "" {
		env = append(env, "GOOS="+platform.goos, "GOARCH="+platform.goarch, "CGO_ENABLED=0")
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports |
			packages.NeedDeps | packages.NeedCompiledGoFiles | packages.NeedModule,
		Dir:   root,
		Tests: false,
		Env:   env,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("packages.Load for %s/%s: %w", platform.goos, platform.goarch, err)
	}
	return pkgs, nil
}

// scanPackages walks the loaded packages and returns every audited call site,
// skipping the packages excluded because importing the reporting package
// would create an import cycle.
func scanPackages(pkgs []*packages.Package, excludedPackages map[string]bool, root string, opts scanOptions) ([]Site, error) {
	var sites []Site
	for _, pkg := range pkgs {
		if excludedPackages[pkg.PkgPath] {
			continue
		}
		if len(pkg.Syntax) != len(pkg.CompiledGoFiles) {
			return nil, fmt.Errorf("%s: %d syntax trees for %d compiled files", pkg.PkgPath, len(pkg.Syntax), len(pkg.CompiledGoFiles))
		}
		if pkg.Module == nil {
			continue
		}
		pkgSites, err := scanPackageFiles(pkg, root, opts)
		if err != nil {
			return nil, err
		}
		sites = append(sites, pkgSites...)
	}
	return sites, nil
}

// scanPackageFiles scans one package's compiled files, reporting each site's
// file path relative to root.
func scanPackageFiles(pkg *packages.Package, root string, opts scanOptions) ([]Site, error) {
	var sites []Site
	for i, file := range pkg.Syntax {
		filename := pkg.CompiledGoFiles[i]
		rel, err := filepath.Rel(root, filename)
		if err != nil {
			return nil, fmt.Errorf("relative path for %s: %w", filename, err)
		}
		relSlash := filepath.ToSlash(rel)
		// Exclusion patterns are repo-relative (e.g. "/testdata/"), so
		// match against relSlash, not the absolute filename: an absolute
		// checkout path containing one of these substrings (e.g. a
		// clone under a directory named "testdata") would otherwise
		// exclude every file and silently return an empty inventory.
		if excluded("/"+relSlash, opts.exclude) {
			continue
		}
		sites = append(sites, scanFile(pkg, file, relSlash, opts)...)
	}
	return sites, nil
}

// reportingDependencies returns the reporting package and its transitive
// dependencies. None of these packages can import the reporting package
// without creating an import cycle, so their log sites cannot adopt the API.
// An empty reportingPath disables this production-only exclusion for fixtures.
func reportingDependencies(pkgs []*packages.Package, reportingPath string) (map[string]bool, error) {
	excluded := make(map[string]bool)
	if reportingPath == "" {
		return excluded, nil
	}
	var reporting *packages.Package
	for _, pkg := range pkgs {
		if pkg.PkgPath == reportingPath {
			reporting = pkg
			break
		}
	}
	if reporting == nil {
		return nil, fmt.Errorf("reporting package %q was not loaded", reportingPath)
	}
	var visit func(*packages.Package)
	visit = func(pkg *packages.Package) {
		if excluded[pkg.PkgPath] {
			return
		}
		excluded[pkg.PkgPath] = true
		for _, imported := range pkg.Imports {
			visit(imported)
		}
	}
	visit(reporting)
	return excluded, nil
}

// packageInit is the reported enclosing function for calls outside any
// function declaration, e.g. inside a package-level variable initializer's
// function literal (ddtrace/tracer/time_windows.go's `var now = func() ...`
// is the canonical in-repo example).
const packageInit = "(package-init)"

// scanFile returns every audited call site in one file. Calls are visited
// everywhere they can appear, not only inside function declarations: a
// package-level initializer's function literal is a real log-call context in
// this repository and must not be dropped from the inventory.
func scanFile(pkg *packages.Package, file *ast.File, relPath string, opts scanOptions) []Site {
	var out []Site
	v := &callVisitor{
		fset:      pkg.Fset,
		info:      pkg.TypesInfo,
		pkg:       pkg,
		opts:      opts,
		relPath:   relPath,
		sites:     &out,
		enclosing: packageInit,
	}
	ast.Walk(v, file)
	applyIgnoreDirectives(out, ignoreDirectiveLines(file, pkg.Fset))
	return out
}

// callVisitor records audited log calls while tracking the enclosing
// function for each one. Visit returns a derived visitor for a function
// declaration's children, which is how ast.Walk propagates context.
type callVisitor struct {
	fset      *token.FileSet
	info      *types.Info
	pkg       *packages.Package
	opts      scanOptions
	relPath   string
	sites     *[]Site
	enclosing string
}

func (v *callVisitor) withEnclosing(name string) *callVisitor {
	c := *v
	c.enclosing = name
	return &c
}

func (v *callVisitor) Visit(n ast.Node) ast.Visitor {
	switch t := n.(type) {
	case *ast.FuncDecl:
		return v.withEnclosing(funcName(t))
	case *ast.CallExpr:
		level, recognized := logCall(v.info, t, v.opts)
		if !recognized {
			return v
		}
		pos := v.fset.Position(t.Pos())
		start := pos.Line
		end := v.fset.Position(t.End()).Line
		site := Site{
			File:    v.relPath,
			Line:    start,
			Package: moduleRel(v.pkg),
			Func:    v.enclosing,
			Level:   level,
			Message: "",
			column:  pos.Column,
			endLine: end,
		}
		if len(t.Args) > 0 {
			site.Message = resolveMessage(v.info, t.Args[0])
		}
		*v.sites = append(*v.sites, site)
	}
	return v
}

// packageErrors collects the load/parse/type errors go/packages reports on
// pkgs and everything they transitively import. A type error in a dependency
// leaves the importing root package IllTyped with no Errors of its own, so
// walking only pkgs would miss it and let the scan proceed on an incomplete
// package graph.
// maxReportedLoadErrors caps how many load errors the failure message
// carries: one broken dependency cascades into hundreds of errors across the
// package graph, and the first few identify the cause.
const maxReportedLoadErrors = 5

func packageErrors(pkgs []*packages.Package) []error {
	var errs []error
	packages.Visit(pkgs, nil, func(pkg *packages.Package) {
		for _, e := range pkg.Errors {
			errs = append(errs, e)
		}
	})
	if len(errs) > maxReportedLoadErrors {
		errs = errs[:maxReportedLoadErrors]
	}
	return errs
}

// moduleRel returns pkg's import path relative to its module root.
func moduleRel(pkg *packages.Package) string {
	if pkg.Module == nil {
		return pkg.PkgPath
	}
	return strings.TrimPrefix(pkg.PkgPath, pkg.Module.Path+"/")
}

// logCall reports whether call is a call to the log package's Error or Warn,
// returning the audited level.
func logCall(info *types.Info, call *ast.CallExpr, opts scanOptions) (string, bool) {
	obj, ok := calleeFunc(info, call)
	if !ok {
		return "", false
	}
	if obj.Pkg() == nil || obj.Pkg().Path() != opts.logPackagePath {
		return "", false
	}
	switch obj.Name() {
	case "Error":
		return levelError, true
	case "Warn":
		return levelWarn, true
	}
	return "", false
}

// calleeFunc resolves the callee of call to a plain (non-method) function
// object, if it is one. It resolves through type information rather than the
// textual selector, so arbitrary import aliases and dot imports of the log
// package are identified reliably, while same-named methods, struct fields,
// and functions in other packages are not.
func calleeFunc(info *types.Info, call *ast.CallExpr) (*types.Func, bool) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		obj, ok := info.Uses[fun].(*types.Func)
		if !ok {
			return nil, false
		}
		return obj, plainFunc(obj)
	case *ast.SelectorExpr:
		obj, ok := info.Uses[fun.Sel].(*types.Func)
		if !ok {
			return nil, false
		}
		return obj, plainFunc(obj)
	}
	return nil, false
}

func plainFunc(obj *types.Func) bool {
	sig, ok := obj.Type().(*types.Signature)
	return ok && sig.Recv() == nil
}

// resolveMessage returns the constant string value of expr, or
// "(non-constant)" when the first argument is not a compile-time constant
// (the constantlogmsg analyzer rejects those sites anyway).
func resolveMessage(info *types.Info, expr ast.Expr) string {
	if tv, ok := info.Types[expr]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
		return constant.StringVal(tv.Value)
	}
	return "(non-constant)"
}

// funcName renders the enclosing function for the report: "Type.Method" for
// methods and the bare name for functions.
func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	return "(" + types.ExprString(fn.Recv.List[0].Type) + ")." + fn.Name.Name
}
