// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package instrumentation

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
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
		// otelc hook modules call into the contrib, which loads telemetry itself.
		if filepath.Base(filepath.Dir(path)) == "otelc" {
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
			found := slices.Contains(pkg.Imports, importStr)
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

	lines := strings.SplitSeq(string(data), "\n")
	for line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module"))
		}
	}
	return ""
}

func hasInstrumentationImport(p contribPkg) bool {
	return slices.Contains(p.Imports, "github.com/DataDog/dd-trace-go/v2/instrumentation")
}

type contribPkg struct {
	ImportPath string
	Imports    []string
}

// TestNoSetTagServiceName ensures that no non-test Go files in contrib/ call SetTag(ext.ServiceName, ...) directly.
// Setting the service name via SetTag overwrites the serviceSource to "m" (manual), which clobbers
// the proper source set by instrumentation.ServiceNameWithSource. Contribs should use
// ServiceNameWithSource at span creation time instead.
func TestNoSetTagServiceName(t *testing.T) {
	root, err := filepath.Abs("../contrib")
	if err != nil {
		t.Fatal(err)
	}

	var offending []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") || base == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		node, parseErr := parser.ParseFile(fset, path, nil, parser.AllErrors)
		if parseErr != nil {
			return nil
		}

		// Check if this file imports the ext package
		extAlias := ""
		for _, imp := range node.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if importPath == "github.com/DataDog/dd-trace-go/v2/ddtrace/ext" {
				if imp.Name != nil {
					extAlias = imp.Name.Name
				} else {
					extAlias = "ext"
				}
				break
			}
		}
		if extAlias == "" {
			return nil
		}

		// Walk the AST looking for .SetTag(ext.ServiceName, ...) calls
		ast.Inspect(node, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 1 {
				return true
			}
			// Check that the function is *.SetTag
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "SetTag" {
				return true
			}
			// Check that the first argument is ext.ServiceName
			argSel, ok := call.Args[0].(*ast.SelectorExpr)
			if !ok {
				return true
			}
			argIdent, ok := argSel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if argIdent.Name == extAlias && argSel.Sel.Name == "ServiceName" {
				relPath, _ := filepath.Rel(root, path)
				pos := fset.Position(call.Pos())
				offending = append(offending, fmt.Sprintf("%s:%d", relPath, pos.Line))
			}
			return true
		})

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(offending) > 0 {
		t.Errorf("Found %d file(s) calling SetTag(ext.ServiceName, ...) directly in contrib/. "+
			"This overwrites the service source to manual (\"m\"), clobbering the source set by "+
			"instrumentation.ServiceNameWithSource. Use ServiceNameWithSource at span creation instead.\n"+
			"Offending locations:\n  %s",
			len(offending), strings.Join(offending, "\n  "))
	}
}

// TestNoTracerServiceName ensures that no non-test Go files call tracer.ServiceName directly.
// All contrib packages should use the instrumentation.ServiceNameWithSource API instead,
// which includes provenance information about where the service name was set.
// This allows the tracer to make better decisions about service name precedence.
func TestNoTracerServiceName(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}

	var offending []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip hidden directories and vendor
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") || base == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		node, parseErr := parser.ParseFile(fset, path, nil, parser.AllErrors)
		if parseErr != nil {
			return nil
		}

		// Check if this file imports the tracer package
		tracerAlias := ""
		for _, imp := range node.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if importPath == "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer" {
				if imp.Name != nil {
					tracerAlias = imp.Name.Name
				} else {
					tracerAlias = "tracer"
				}
				break
			}
		}
		if tracerAlias == "" {
			return nil
		}

		// Walk the AST looking for tracer.ServiceName calls
		ast.Inspect(node, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if ident.Name == tracerAlias && sel.Sel.Name == "ServiceName" {
				relPath, _ := filepath.Rel(root, path)
				pos := fset.Position(sel.Pos())
				offending = append(offending, fmt.Sprintf("%s:%d", relPath, pos.Line))
			}
			return true
		})

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(offending) > 0 {
		t.Errorf("Found %d file(s) using tracer.ServiceName directly. "+
			"All contrib packages should use instrumentation.ServiceNameWithSource instead, "+
			"which provides service name source information for better precedence handling.\n"+
			"Offending locations:\n  %s",
			len(offending), strings.Join(offending, "\n  "))
	}
}

// TestManifestNameMatchesTracedPackage fails when the top-level name of a contrib manifest.yaml
// is not the TracedPackage of the Package registered for that contrib path in packages.go, or
// when two manifests share a name. The manifest name identifies the integration (the instrumented
// library), whereas packages[].name lists the published packages and may legitimately differ.
//
// Origin: reviewers repeatedly had to ask for a new manifest's name to be changed to the
// integration's TracedPackage, "like every other manifest", which is mechanical to check.
func TestManifestNameMatchesTracedPackage(t *testing.T) {
	root, err := filepath.Abs("../contrib")
	if err != nil {
		t.Fatal(err)
	}

	packagesMu.RLock()
	tracedByPath := make(map[string]string, len(packages))
	for pkg, info := range packages {
		tracedByPath[string(pkg)] = info.TracedPackage
	}
	packagesMu.RUnlock()

	var manifests []contribManifest
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") || base == "vendor" || base == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "manifest.yaml" {
			return nil
		}
		relPath, _ := filepath.Rel(root, path)
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var m struct {
			Name string `yaml:"name"`
		}
		if err := yaml.Unmarshal(raw, &m); err != nil {
			return fmt.Errorf("%s: %w", relPath, err)
		}
		manifests = append(manifests, contribManifest{relPath: relPath, name: m.Name})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) == 0 {
		t.Fatal("no contrib manifest.yaml files found; the walk root is probably wrong")
	}

	offending := checkManifestNames(tracedByPath, manifests)
	if len(offending) > 0 {
		t.Errorf("Found %d contrib manifest problem(s). Set name to this integration's TracedPackage "+
			"in instrumentation/packages.go (the Package key is the contrib path); names must be unique. "+
			"The published packages belong under packages[].name.\nOffending manifests:\n  %s",
			len(offending), strings.Join(offending, "\n  "))
	}
}

type contribManifest struct {
	relPath string
	name    string
}

// checkManifestNames reports manifests whose top-level name is not the TracedPackage of the
// Package registered for that contrib path, or whose name is reused by another manifest.
func checkManifestNames(tracedByPath map[string]string, manifests []contribManifest) []string {
	var offending []string
	byName := make(map[string][]string)
	for _, m := range manifests {
		relPath := filepath.ToSlash(m.relPath)
		contribPath := filepath.ToSlash(filepath.Dir(filepath.FromSlash(relPath)))
		byName[m.name] = append(byName[m.name], relPath)
		traced, ok := tracedByPath[contribPath]
		if !ok {
			offending = append(offending, fmt.Sprintf("%s: no Package in instrumentation/packages.go for contrib path %q", relPath, contribPath))
			continue
		}
		if m.name != traced {
			offending = append(offending, fmt.Sprintf("%s: name %q, want TracedPackage %q", relPath, m.name, traced))
		}
	}
	for name, paths := range byName {
		if len(paths) < 2 {
			continue
		}
		slices.Sort(paths)
		offending = append(offending, fmt.Sprintf("duplicate name %q used by %s", name, strings.Join(paths, ", ")))
	}
	slices.Sort(offending)
	return offending
}

func TestCheckManifestNames(t *testing.T) {
	traced := map[string]string{
		"gorilla/mux": "github.com/gorilla/mux",
		"foo/bar":     "github.com/foo/bar",
	}
	tests := []struct {
		name      string
		traced    map[string]string
		manifests []contribManifest
		want      []string
	}{
		{
			name:   "matching unique names",
			traced: traced,
			manifests: []contribManifest{
				{relPath: "gorilla/mux/manifest.yaml", name: "github.com/gorilla/mux"},
				{relPath: "foo/bar/manifest.yaml", name: "github.com/foo/bar"},
			},
		},
		{
			name:   "copied name from another integration",
			traced: traced,
			manifests: []contribManifest{
				{relPath: "gorilla/mux/manifest.yaml", name: "github.com/gorilla/mux"},
				{relPath: "foo/bar/manifest.yaml", name: "github.com/gorilla/mux"},
			},
			want: []string{
				`duplicate name "github.com/gorilla/mux" used by foo/bar/manifest.yaml, gorilla/mux/manifest.yaml`,
				`foo/bar/manifest.yaml: name "github.com/gorilla/mux", want TracedPackage "github.com/foo/bar"`,
			},
		},
		{
			name:   "copied TracedPackage as well as name",
			traced: map[string]string{"gorilla/mux": "github.com/gorilla/mux", "foo/bar": "github.com/gorilla/mux"},
			manifests: []contribManifest{
				{relPath: "gorilla/mux/manifest.yaml", name: "github.com/gorilla/mux"},
				{relPath: "foo/bar/manifest.yaml", name: "github.com/gorilla/mux"},
			},
			want: []string{
				`duplicate name "github.com/gorilla/mux" used by foo/bar/manifest.yaml, gorilla/mux/manifest.yaml`,
			},
		},
		{
			name:   "missing packages.go entry",
			traced: traced,
			manifests: []contribManifest{
				{relPath: "new/lib/manifest.yaml", name: "github.com/new/lib"},
			},
			want: []string{
				`new/lib/manifest.yaml: no Package in instrumentation/packages.go for contrib path "new/lib"`,
			},
		},
		{
			name:   "lone wrong name",
			traced: traced,
			manifests: []contribManifest{
				{relPath: filepath.FromSlash("foo/bar/manifest.yaml"), name: "github.com/old/typo"},
			},
			want: []string{
				`foo/bar/manifest.yaml: name "github.com/old/typo", want TracedPackage "github.com/foo/bar"`,
			},
		},
		{
			name:   "empty name",
			traced: traced,
			manifests: []contribManifest{
				{relPath: "foo/bar/manifest.yaml", name: ""},
			},
			want: []string{
				`foo/bar/manifest.yaml: name "", want TracedPackage "github.com/foo/bar"`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkManifestNames(tt.traced, tt.manifests)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("problems mismatch\ngot:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(tt.want, "\n  "))
			}
		})
	}
}

// TestNoTypedComponentTag ensures that no non-test Go files in contrib/ or instrumentation/ pass an
// instrumentation.Package value to Tag(ext.Component, ...) or SetTag(ext.Component, ...).
// Span.setTagLocked only records the span integration when the ext.Component value is a plain
// string. Passing a Package value still sets the component tag, but the tracer counts the span as
// integration:manual in the spans_started and spans_finished metrics. Fix a violation with string(...).
//
// The check is syntactic. It flags inline <instrumentation>.PackageXxx selectors,
// instrumentation.Package(...) conversions, and identifiers declared in package or lexical scope
// with an instrumentation.Package type or initializer. It cannot see through struct fields or
// constants exported by another package.
//
// The baseline map pins the exact match count for files that already had this bug when the check
// was added. Fixing those files is out of scope here. The test fails when a file exceeds its
// baseline count or drops below it. Lower or remove an entry when you fix pre-existing calls.
func TestNoTypedComponentTag(t *testing.T) {
	baseline := map[string]int{
		"contrib/99designs/gqlgen/tracer.go":                    3,
		"contrib/IBM/sarama/dispatcher.go":                      1,
		"contrib/IBM/sarama/producer.go":                        1,
		"contrib/Shopify/sarama/sarama.go":                      2,
		"contrib/database/sql/conn.go":                          1,
		"contrib/go.mongodb.org/mongo-driver.v2/mongo/mongo.go": 1,
		"contrib/gocql/gocql/gocql.go":                          1,
		"contrib/gorilla/mux/mux.go":                            1,
		"contrib/gorm.io/gorm.v1/gorm.go":                       1,
		"contrib/graph-gophers/graphql-go/graphql.go":           2,
		"contrib/graphql-go/graphql/graphql.go":                 5,
		"contrib/hashicorp/consul/consul.go":                    1,
		"contrib/jackc/pgx.v5/pgx_tracer.go":                    1,
		"contrib/labstack/echo.v4/echotrace.go":                 1,
		"contrib/labstack/echo.v5/echotrace.go":                 1,
		"contrib/miekg/dns/dns.go":                              1,
		"contrib/redis/go-redis.v9/redis.go":                    1,
		"contrib/redis/rueidis/rueidis.go":                      1,
		"contrib/tidwall/buntdb/buntdb.go":                      1,
		"contrib/twitchtv/twirp/twirp.go":                       3,
		"contrib/uptrace/bun/bun.go":                            1,
		"contrib/urfave/negroni/negroni.go":                     1,
		"contrib/valkey-io/valkey-go/valkey.go":                 1,
		"contrib/valyala/fasthttp/fasthttp.go":                  1,

		// Fixture file pinning the matcher against known-bad and safe patterns.
		"instrumentation/testdata/typed_component_tag.go": 8,
	}

	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}

	report, err := checkTypedComponentTags(root, []string{"contrib", "instrumentation"}, baseline)
	if err != nil {
		t.Fatal(err)
	}

	if len(report.offending) > 0 {
		t.Errorf("Found %d call(s) passing an instrumentation.Package value as the ext.Component tag. "+
			"The tracer only records a span integration when the component is a plain string, so these "+
			"spans are counted as manual. Convert the value with string(...) instead.\n"+
			"Offending locations:\n  %s",
			len(report.offending), strings.Join(report.offending, "\n  "))
	}

	if len(report.stale) > 0 {
		t.Errorf("Found %d baseline entry(ies) whose match count dropped below baseline. "+
			"Lower or remove them in TestNoTypedComponentTag:\n  %s",
			len(report.stale), strings.Join(report.stale, "\n  "))
	}
}

type typedComponentTagReport struct {
	offending []string
	stale     []string
	counts    map[string]int
}

type typedComponentGoFile struct {
	path       string // Slash-separated and relative to root.
	dir        string
	fset       *token.FileSet
	node       *ast.File
	extAlias   string
	instrAlias string
}

type typedComponentPkgKey struct {
	dir string
	pkg string
}

// checkTypedComponentTags scans non-test Go files under dirs relative to root for
// Tag(ext.Component, ...) and SetTag(ext.Component, ...) calls that pass an
// instrumentation.Package value. It compares per-file match counts against baseline.
func checkTypedComponentTags(root string, dirs []string, baseline map[string]int) (typedComponentTagReport, error) {
	var files []typedComponentGoFile
	for _, dir := range dirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				base := filepath.Base(path)
				if strings.HasPrefix(base, ".") || base == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			fset := token.NewFileSet()
			node, parseErr := parser.ParseFile(fset, path, nil, parser.AllErrors)
			if parseErr != nil {
				return nil
			}
			relPath, _ := filepath.Rel(root, path)
			files = append(files, typedComponentGoFile{
				path:       filepath.ToSlash(relPath),
				dir:        filepath.ToSlash(filepath.Dir(relPath)),
				fset:       fset,
				node:       node,
				extAlias:   importName(node, "github.com/DataDog/dd-trace-go/v2/ddtrace/ext", "ext"),
				instrAlias: importName(node, "github.com/DataDog/dd-trace-go/v2/instrumentation", "instrumentation"),
			})
			return nil
		})
		if err != nil {
			return typedComponentTagReport{}, err
		}
	}

	pkgTyped := collectPackageTypedIdents(files)

	report := typedComponentTagReport{
		counts: make(map[string]int),
	}
	for _, f := range files {
		if f.extAlias == "" {
			continue
		}
		key := typedComponentPkgKey{dir: f.dir, pkg: f.node.Name.Name}
		w := &typedComponentWalker{
			file:   f,
			scopes: typedScopeStack{pkg: pkgTyped[key]},
		}
		w.walk(f.node)
		for _, line := range w.matchLines {
			report.counts[f.path]++
			if report.counts[f.path] > baseline[f.path] {
				report.offending = append(report.offending, fmt.Sprintf("%s:%d", f.path, line))
			}
		}
	}

	for path, expected := range baseline {
		if expected <= 0 || report.counts[path] < expected {
			report.stale = append(report.stale, fmt.Sprintf("%s (got %d, baseline %d)", path, report.counts[path], expected))
		}
	}
	slices.Sort(report.stale)

	return report, nil
}

func isPackageType(f typedComponentGoFile, expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	switch e := ast.Unparen(expr).(type) {
	case *ast.Ident:
		return f.dir == "instrumentation" && e.Name == "Package"
	case *ast.SelectorExpr:
		x, ok := ast.Unparen(e.X).(*ast.Ident)
		return ok && f.instrAlias != "" && x.Name == f.instrAlias && e.Sel.Name == "Package"
	}
	return false
}

func isPackageExpr(f typedComponentGoFile, expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	switch e := ast.Unparen(expr).(type) {
	case *ast.SelectorExpr:
		x, ok := ast.Unparen(e.X).(*ast.Ident)
		return ok && f.instrAlias != "" && x.Name == f.instrAlias && strings.HasPrefix(e.Sel.Name, "Package") && e.Sel.Name != "Package"
	case *ast.CallExpr:
		return len(e.Args) == 1 && isPackageType(f, e.Fun)
	}
	return false
}

// collectPackageTypedIdents collects package-level const and var declarations typed as
// instrumentation.Package across all files in each package.
func collectPackageTypedIdents(files []typedComponentGoFile) map[typedComponentPkgKey]map[string]bool {
	pkgTyped := make(map[typedComponentPkgKey]map[string]bool)
	for _, f := range files {
		key := typedComponentPkgKey{dir: f.dir, pkg: f.node.Name.Name}
		if pkgTyped[key] == nil {
			pkgTyped[key] = make(map[string]bool)
		}
	}

	// Repeat until convergence so package-level aliases resolve regardless of file order.
	for changed := true; changed; {
		changed = false
		for _, f := range files {
			key := typedComponentPkgKey{dir: f.dir, pkg: f.node.Name.Name}
			idents := pkgTyped[key]
			for _, decl := range f.node.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || (gen.Tok != token.CONST && gen.Tok != token.VAR) {
					continue
				}
				var prevType ast.Expr
				var prevValues []ast.Expr
				for _, s := range gen.Specs {
					spec, ok := s.(*ast.ValueSpec)
					if !ok {
						continue
					}
					specType := spec.Type
					specValues := spec.Values
					if gen.Tok == token.CONST {
						if specType == nil && len(specValues) == 0 {
							specType = prevType
							specValues = prevValues
						} else {
							prevType = specType
							prevValues = specValues
						}
					}
					for i, name := range spec.Names {
						if name.Name == "_" || idents[name.Name] {
							continue
						}
						isPkg := false
						if specType != nil {
							isPkg = isPackageType(f, specType)
						} else if i < len(specValues) {
							val := ast.Unparen(specValues[i])
							if isPackageExpr(f, val) {
								isPkg = true
							} else if rhsIdent, ok := val.(*ast.Ident); ok && idents[rhsIdent.Name] {
								isPkg = true
							}
						}
						if isPkg {
							idents[name.Name] = true
							changed = true
						}
					}
				}
			}
		}
	}
	return pkgTyped
}

type typedScopeStack struct {
	pkg    map[string]bool
	scopes []map[string]bool
}

func (s *typedScopeStack) push() {
	s.scopes = append(s.scopes, make(map[string]bool))
}

func (s *typedScopeStack) pop() {
	s.scopes = s.scopes[:len(s.scopes)-1]
}

func (s *typedScopeStack) bind(name string, isPkg bool) {
	if name == "" || name == "_" || len(s.scopes) == 0 {
		return
	}
	s.scopes[len(s.scopes)-1][name] = isPkg
}

func (s *typedScopeStack) lookup(name string) bool {
	for _, scope := range slices.Backward(s.scopes) {
		if val, ok := scope[name]; ok {
			return val
		}
	}
	return s.pkg[name]
}

type typedComponentWalker struct {
	file       typedComponentGoFile
	scopes     typedScopeStack
	matchLines []int
}

func (w *typedComponentWalker) bindFieldList(fl *ast.FieldList) {
	if fl == nil {
		return
	}
	for _, field := range fl.List {
		isPkg := isPackageType(w.file, field.Type)
		for _, name := range field.Names {
			w.scopes.bind(name.Name, isPkg)
		}
	}
}

func (w *typedComponentWalker) isPackageValue(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	unwrapped := ast.Unparen(expr)
	if isPackageExpr(w.file, unwrapped) {
		return true
	}
	if ident, ok := unwrapped.(*ast.Ident); ok {
		return w.scopes.lookup(ident.Name)
	}
	return false
}

func (w *typedComponentWalker) walk(n ast.Node) {
	if n == nil {
		return
	}
	ast.Inspect(n, func(cur ast.Node) bool {
		if cur == nil {
			return false
		}
		return w.visit(cur)
	})
}

func (w *typedComponentWalker) visit(cur ast.Node) bool {
	switch n := cur.(type) {
	case *ast.FuncDecl:
		w.scopes.push()
		w.bindFieldList(n.Recv)
		if n.Type != nil {
			w.bindFieldList(n.Type.Params)
			w.bindFieldList(n.Type.Results)
		}
		if n.Body != nil {
			for _, stmt := range n.Body.List {
				w.walk(stmt)
			}
		}
		w.scopes.pop()
		return false

	case *ast.FuncLit:
		w.scopes.push()
		if n.Type != nil {
			w.bindFieldList(n.Type.Params)
			w.bindFieldList(n.Type.Results)
		}
		if n.Body != nil {
			for _, stmt := range n.Body.List {
				w.walk(stmt)
			}
		}
		w.scopes.pop()
		return false

	case *ast.GenDecl:
		if n.Tok != token.VAR && n.Tok != token.CONST {
			return false
		}
		if len(w.scopes.scopes) == 0 {
			// Package-level bindings are already in w.scopes.pkg. Walk initializers for tag calls.
			for _, s := range n.Specs {
				if spec, ok := s.(*ast.ValueSpec); ok {
					for _, v := range spec.Values {
						w.walk(v)
					}
				}
			}
			return false
		}
		var prevType ast.Expr
		var prevValues []ast.Expr
		for _, s := range n.Specs {
			spec, ok := s.(*ast.ValueSpec)
			if !ok {
				continue
			}
			specType := spec.Type
			specValues := spec.Values
			if n.Tok == token.CONST {
				if specType == nil && len(specValues) == 0 {
					specType = prevType
					specValues = prevValues
				} else {
					prevType = specType
					prevValues = specValues
				}
			}
			for _, v := range spec.Values {
				w.walk(v)
			}
			vals := make([]bool, len(spec.Names))
			for i := range spec.Names {
				if specType != nil {
					vals[i] = isPackageType(w.file, specType)
				} else if i < len(specValues) {
					vals[i] = w.isPackageValue(specValues[i])
				}
			}
			for i, name := range spec.Names {
				w.scopes.bind(name.Name, vals[i])
			}
		}
		return false

	case *ast.BlockStmt:
		w.scopes.push()
		for _, stmt := range n.List {
			w.walk(stmt)
		}
		w.scopes.pop()
		return false

	case *ast.IfStmt:
		w.scopes.push()
		w.walk(n.Init)
		w.walk(n.Cond)
		w.walk(n.Body)
		w.walk(n.Else)
		w.scopes.pop()
		return false

	case *ast.ForStmt:
		w.scopes.push()
		w.walk(n.Init)
		w.walk(n.Cond)
		w.walk(n.Post)
		w.walk(n.Body)
		w.scopes.pop()
		return false

	case *ast.RangeStmt:
		w.scopes.push()
		w.walk(n.X)
		if n.Tok == token.DEFINE {
			if ident, ok := n.Key.(*ast.Ident); ok {
				w.scopes.bind(ident.Name, false)
			}
			if ident, ok := n.Value.(*ast.Ident); ok {
				w.scopes.bind(ident.Name, false)
			}
		} else {
			w.walk(n.Key)
			w.walk(n.Value)
		}
		w.walk(n.Body)
		w.scopes.pop()
		return false

	case *ast.SwitchStmt:
		w.scopes.push()
		w.walk(n.Init)
		w.walk(n.Tag)
		w.walk(n.Body)
		w.scopes.pop()
		return false

	case *ast.TypeSwitchStmt:
		w.scopes.push()
		w.walk(n.Init)
		typeSwitchVar := ""
		if assign, ok := n.Assign.(*ast.AssignStmt); ok && assign.Tok == token.DEFINE {
			for _, rhs := range assign.Rhs {
				w.walk(rhs)
			}
			if len(assign.Lhs) == 1 {
				if ident, ok := assign.Lhs[0].(*ast.Ident); ok && ident.Name != "_" {
					typeSwitchVar = ident.Name
					w.scopes.bind(ident.Name, false)
				}
			}
		} else {
			w.walk(n.Assign)
		}
		if n.Body != nil {
			for _, stmt := range n.Body.List {
				clause, ok := stmt.(*ast.CaseClause)
				if !ok {
					w.walk(stmt)
					continue
				}
				w.scopes.push()
				for _, e := range clause.List {
					w.walk(e)
				}
				if typeSwitchVar != "" && len(clause.List) == 1 && isPackageType(w.file, clause.List[0]) {
					w.scopes.bind(typeSwitchVar, true)
				}
				for _, s := range clause.Body {
					w.walk(s)
				}
				w.scopes.pop()
			}
		}
		w.scopes.pop()
		return false

	case *ast.CaseClause:
		w.scopes.push()
		for _, e := range n.List {
			w.walk(e)
		}
		for _, s := range n.Body {
			w.walk(s)
		}
		w.scopes.pop()
		return false

	case *ast.SelectStmt:
		w.scopes.push()
		w.walk(n.Body)
		w.scopes.pop()
		return false

	case *ast.CommClause:
		w.scopes.push()
		w.walk(n.Comm)
		for _, s := range n.Body {
			w.walk(s)
		}
		w.scopes.pop()
		return false

	case *ast.AssignStmt:
		for _, rhs := range n.Rhs {
			w.walk(rhs)
		}
		if n.Tok == token.DEFINE {
			vals := make([]bool, len(n.Lhs))
			if len(n.Lhs) == len(n.Rhs) {
				for i, rhs := range n.Rhs {
					vals[i] = w.isPackageValue(rhs)
				}
			}
			for i, lhs := range n.Lhs {
				if ident, ok := lhs.(*ast.Ident); ok {
					w.scopes.bind(ident.Name, vals[i])
				}
			}
		} else {
			for _, lhs := range n.Lhs {
				w.walk(lhs)
			}
		}
		return false

	case *ast.CallExpr:
		if len(n.Args) == 2 {
			sel, ok := ast.Unparen(n.Fun).(*ast.SelectorExpr)
			if ok && (sel.Sel.Name == "Tag" || sel.Sel.Name == "SetTag") {
				if key, ok := ast.Unparen(n.Args[0]).(*ast.SelectorExpr); ok {
					if keyIdent, ok := ast.Unparen(key.X).(*ast.Ident); ok && keyIdent.Name == w.file.extAlias && key.Sel.Name == "Component" {
						if w.isPackageValue(n.Args[1]) {
							pos := w.file.fset.Position(n.Pos())
							w.matchLines = append(w.matchLines, pos.Line)
						}
					}
				}
			}
		}
		return true
	}
	return true
}

// importName returns the name under which node imports importPath, defaulting to
// defaultName, or "" if node does not import it.
func importName(node *ast.File, importPath, defaultName string) string {
	for _, imp := range node.Imports {
		if strings.Trim(imp.Path.Value, `"`) != importPath {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return defaultName
	}
	return ""
}

func TestCheckTypedComponentTags(t *testing.T) {
	writeFiles := func(t *testing.T, files map[string]string) string {
		t.Helper()
		root := t.TempDir()
		for rel, content := range files {
			full := filepath.Join(root, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}

	t.Run("lexical_scope_isolation", func(t *testing.T) {
		root := writeFiles(t, map[string]string{
			"contrib/foo/foo.go": `package foo

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

func badFunc(span interface{ SetTag(string, any) }) {
	component := instrumentation.PackageNetHTTP
	span.SetTag(ext.Component, component)
}

func safeSiblingFunc(span interface{ SetTag(string, any) }) {
	component := "net/http"
	span.SetTag(ext.Component, component)
}

func blockAndClosureIsolation(span interface{ SetTag(string, any) }) {
	name := "net/http"
	if true {
		name := instrumentation.PackageNetHTTP
		span.SetTag(ext.Component, name)
	}
	span.SetTag(ext.Component, name)

	fn := func() {
		name := instrumentation.PackageNetHTTP
		span.SetTag(ext.Component, name)
	}
	fn()
	span.SetTag(ext.Component, name)
}
`,
		})

		report, err := checkTypedComponentTags(root, []string{"contrib"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{
			"contrib/foo/foo.go:10",
			"contrib/foo/foo.go:22",
			"contrib/foo/foo.go:28",
		}
		if !slices.Equal(report.offending, want) {
			t.Fatalf("offending = %v, want %v", report.offending, want)
		}
	})

	t.Run("shadowing_both_directions", func(t *testing.T) {
		root := writeFiles(t, map[string]string{
			"contrib/foo/decls.go": `package foo

import "github.com/DataDog/dd-trace-go/v2/instrumentation"

const pkgTyped = instrumentation.PackageNetHTTP
const pkgPlain = "net/http"
`,
			"contrib/foo/use.go": `package foo

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type receiver struct{}

func (pkgTyped receiver) shadowedByReceiver(span interface{ SetTag(string, any) }) {
	span.SetTag(ext.Component, pkgTyped)
}

func shadowedByParam(span interface{ SetTag(string, any) }, pkgTyped string) {
	span.SetTag(ext.Component, pkgTyped)
}

func shadowedByLocalAndInnerBlocks(span interface{ SetTag(string, any) }, items []string, ch <-chan string, v any) {
	if pkgTyped := "net/http"; pkgTyped != "" {
		span.SetTag(ext.Component, pkgTyped)
	} else {
		span.SetTag(ext.Component, pkgTyped)
	}
	// Package-level pkgTyped is active again outside the if scope.
	span.SetTag(ext.Component, pkgTyped)

	for _, pkgTyped := range items {
		span.SetTag(ext.Component, pkgTyped)
	}

	switch pkgTyped := v.(type) {
	case string:
		span.SetTag(ext.Component, pkgTyped)
	case instrumentation.Package:
		span.SetTag(ext.Component, pkgTyped)
	}

	select {
	case pkgTyped := <-ch:
		span.SetTag(ext.Component, pkgTyped)
	default:
	}

	{
		pkgTyped := "net/http"
		derived := pkgTyped
		span.SetTag(ext.Component, derived)
	}
	derivedFromPkg := pkgTyped
	span.SetTag(ext.Component, derivedFromPkg)
}

func shadowPlainWithTyped(span interface{ SetTag(string, any) }, pkgPlain instrumentation.Package) (res instrumentation.Package) {
	span.SetTag(ext.Component, pkgPlain)
	span.SetTag(ext.Component, res)
	return pkgPlain
}

func shadowPlainInBodyAndRedeclare(span interface{ SetTag(string, any) }) {
	pkgPlain := instrumentation.PackageNetHTTP
	span.SetTag(ext.Component, pkgPlain)

	pkgPlain, extra := "net/http", 1
	_ = extra
	span.SetTag(ext.Component, pkgPlain)
}
`,
		})

		report, err := checkTypedComponentTags(root, []string{"contrib"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{
			"contrib/foo/use.go:25",
			"contrib/foo/use.go:35",
			"contrib/foo/use.go:50",
			"contrib/foo/use.go:54",
			"contrib/foo/use.go:55",
			"contrib/foo/use.go:61",
		}
		if !slices.Equal(report.offending, want) {
			t.Fatalf("offending = %v, want %v", report.offending, want)
		}
	})

	t.Run("package_conversions", func(t *testing.T) {
		root := writeFiles(t, map[string]string{
			"contrib/foo/foo.go": `package foo

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	instr "github.com/DataDog/dd-trace-go/v2/instrumentation"
)

func Package(s string) string { return s }

func check(span interface{ SetTag(string, any) }, name string) {
	span.SetTag(ext.Component, instr.Package(name))
	span.SetTag(ext.Component, ((instr.Package)(name)))
	converted := instr.Package(name)
	span.SetTag(ext.Component, converted)
	span.SetTag(ext.Component, Package(name))
	span.SetTag(ext.Component, string(instr.PackageNetHTTP))
}
`,
			"instrumentation/internal_pkg.go": `package instrumentation

import "github.com/DataDog/dd-trace-go/v2/ddtrace/ext"

type Package string

func checkInternal(span interface{ SetTag(string, any) }, name string) {
	span.SetTag(ext.Component, Package(name))
	span.SetTag(ext.Component, ((Package)(name)))
	span.SetTag(ext.Component, string(Package(name)))
}
`,
		})

		report, err := checkTypedComponentTags(root, []string{"contrib", "instrumentation"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{
			"contrib/foo/foo.go:11",
			"contrib/foo/foo.go:12",
			"contrib/foo/foo.go:14",
			"instrumentation/internal_pkg.go:8",
			"instrumentation/internal_pkg.go:9",
		}
		if !slices.Equal(report.offending, want) {
			t.Fatalf("offending = %v, want %v", report.offending, want)
		}
	})

	t.Run("baseline_counts", func(t *testing.T) {
		root := writeFiles(t, map[string]string{
			"contrib/foo/foo.go": `package foo

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

func f(span interface{ SetTag(string, any) }) {
	span.SetTag(ext.Component, instrumentation.PackageNetHTTP)
	span.SetTag(ext.Component, instrumentation.PackageNetHTTP)
}
`,
			"contrib/bar/bar.go": `package bar

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

func f(span interface{ SetTag(string, any) }) {
	span.SetTag(ext.Component, string(instrumentation.PackageNetHTTP))
}
`,
		})

		// Exact baseline passes with no offending or stale entries.
		exact, err := checkTypedComponentTags(root, []string{"contrib"}, map[string]int{
			"contrib/foo/foo.go": 2,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(exact.offending) != 0 || len(exact.stale) != 0 {
			t.Fatalf("exact baseline: offending = %v, stale = %v, want both empty", exact.offending, exact.stale)
		}

		// Exceeding baseline reports only matches beyond the pinned count.
		exceeded, err := checkTypedComponentTags(root, []string{"contrib"}, map[string]int{
			"contrib/foo/foo.go": 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		wantOffending := []string{"contrib/foo/foo.go:10"}
		if !slices.Equal(exceeded.offending, wantOffending) {
			t.Fatalf("exceeded baseline: offending = %v, want %v", exceeded.offending, wantOffending)
		}
		if len(exceeded.stale) != 0 {
			t.Fatalf("exceeded baseline: stale = %v, want empty", exceeded.stale)
		}

		// Report lowered or zero match counts as stale.
		stale, err := checkTypedComponentTags(root, []string{"contrib"}, map[string]int{
			"contrib/foo/foo.go": 3,
			"contrib/bar/bar.go": 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(stale.offending) != 0 {
			t.Fatalf("stale baseline: offending = %v, want empty", stale.offending)
		}
		wantStale := []string{
			"contrib/bar/bar.go (got 0, baseline 1)",
			"contrib/foo/foo.go (got 2, baseline 3)",
		}
		if !slices.Equal(stale.stale, wantStale) {
			t.Fatalf("stale baseline: stale = %v, want %v", stale.stale, wantStale)
		}
	})
}
