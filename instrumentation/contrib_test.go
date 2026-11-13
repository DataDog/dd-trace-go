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

// TestNoTypedComponentTag ensures that no non-test Go files in contrib/ or instrumentation/ pass an
// instrumentation.Package value to Tag(ext.Component, ...) or SetTag(ext.Component, ...).
// Span.setTagLocked only records the span's integration when the ext.Component value is a plain
// string, so a Package value still produces the component tag but the span is counted as
// integration:manual in the spans_started/spans_finished metrics. Fix a violation with string(...).
//
// The check is syntactic. It flags inline <instrumentation>.PackageXxx selectors, and identifiers
// declared in the same package with an instrumentation.Package type or initializer. It cannot see
// through struct fields, function parameters, or constants exported by another package.
//
// The allowlist holds the files that already had this bug when the check was added; fixing them is
// out of scope here. The test fails when an allowlisted file no longer matches, so remove its entry
// once it is fixed. Only add an entry for a match that is not actually an instrumentation.Package
// value, with a comment explaining why.
func TestNoTypedComponentTag(t *testing.T) {
	allowlist := map[string]bool{
		"contrib/99designs/gqlgen/tracer.go":                    true,
		"contrib/IBM/sarama/dispatcher.go":                      true,
		"contrib/IBM/sarama/producer.go":                        true,
		"contrib/Shopify/sarama/sarama.go":                      true,
		"contrib/database/sql/conn.go":                          true,
		"contrib/go.mongodb.org/mongo-driver.v2/mongo/mongo.go": true,
		"contrib/gocql/gocql/gocql.go":                          true,
		"contrib/gorilla/mux/mux.go":                            true,
		"contrib/gorm.io/gorm.v1/gorm.go":                       true,
		"contrib/graph-gophers/graphql-go/graphql.go":           true,
		"contrib/graphql-go/graphql/graphql.go":                 true,
		"contrib/hashicorp/consul/consul.go":                    true,
		"contrib/jackc/pgx.v5/pgx_tracer.go":                    true,
		"contrib/labstack/echo.v4/echotrace.go":                 true,
		"contrib/labstack/echo.v5/echotrace.go":                 true,
		"contrib/miekg/dns/dns.go":                              true,
		"contrib/redis/go-redis.v9/redis.go":                    true,
		"contrib/redis/rueidis/rueidis.go":                      true,
		"contrib/tidwall/buntdb/buntdb.go":                      true,
		"contrib/twitchtv/twirp/twirp.go":                       true,
		"contrib/uptrace/bun/bun.go":                            true,
		"contrib/urfave/negroni/negroni.go":                     true,
		"contrib/valkey-io/valkey-go/valkey.go":                 true,
		"contrib/valyala/fasthttp/fasthttp.go":                  true,
	}

	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}

	type goFile struct {
		path       string // Slash-separated and relative to root.
		dir        string
		fset       *token.FileSet
		node       *ast.File
		extAlias   string
		instrAlias string
	}
	var files []goFile
	for _, dir := range []string{"contrib", "instrumentation"} {
		err = filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
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
			files = append(files, goFile{
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
			t.Fatal(err)
		}
	}

	isPackageType := func(f goFile, expr ast.Expr) bool {
		switch e := expr.(type) {
		case *ast.Ident:
			return f.dir == "instrumentation" && e.Name == "Package"
		case *ast.SelectorExpr:
			x, ok := e.X.(*ast.Ident)
			return ok && f.instrAlias != "" && x.Name == f.instrAlias && e.Sel.Name == "Package"
		}
		return false
	}
	isPackageConst := func(f goFile, expr ast.Expr) bool {
		sel, ok := ast.Unparen(expr).(*ast.SelectorExpr)
		if !ok {
			return false
		}
		x, ok := sel.X.(*ast.Ident)
		return ok && f.instrAlias != "" && x.Name == f.instrAlias && strings.HasPrefix(sel.Sel.Name, "Package")
	}

	// Collect typed identifiers per package rather than per file: a package-level constant is
	// often declared in a different file from the span code that uses it.
	typedIdents := make(map[string]map[string]bool)
	for _, f := range files {
		idents := typedIdents[f.dir]
		if idents == nil {
			idents = make(map[string]bool)
			typedIdents[f.dir] = idents
		}
		ast.Inspect(f.node, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ValueSpec:
				for i, name := range n.Names {
					if isPackageType(f, n.Type) || (i < len(n.Values) && isPackageConst(f, n.Values[i])) {
						idents[name.Name] = true
					}
				}
			case *ast.AssignStmt:
				if n.Tok != token.DEFINE || len(n.Lhs) != len(n.Rhs) {
					return true
				}
				for i, lhs := range n.Lhs {
					if ident, ok := lhs.(*ast.Ident); ok && isPackageConst(f, n.Rhs[i]) {
						idents[ident.Name] = true
					}
				}
			}
			return true
		})
	}

	var offending []string
	matched := make(map[string]bool)
	for _, f := range files {
		if f.extAlias == "" {
			continue
		}
		// Walk the AST looking for .Tag(ext.Component, v) and .SetTag(ext.Component, v) calls
		ast.Inspect(f.node, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Tag" && sel.Sel.Name != "SetTag") {
				return true
			}
			key, ok := call.Args[0].(*ast.SelectorExpr)
			if !ok {
				return true
			}
			keyIdent, ok := key.X.(*ast.Ident)
			if !ok || keyIdent.Name != f.extAlias || key.Sel.Name != "Component" {
				return true
			}
			value := ast.Unparen(call.Args[1])
			ident, isIdent := value.(*ast.Ident)
			if !isPackageConst(f, value) && !(isIdent && typedIdents[f.dir][ident.Name]) {
				return true
			}
			matched[f.path] = true
			if !allowlist[f.path] {
				pos := f.fset.Position(call.Pos())
				offending = append(offending, fmt.Sprintf("%s:%d", f.path, pos.Line))
			}
			return true
		})
	}

	if len(offending) > 0 {
		t.Errorf("Found %d call(s) passing an instrumentation.Package value as the ext.Component tag. "+
			"The tracer only records a span's integration when the component is a plain string, so these "+
			"spans are counted as manual. Convert the value with string(...) instead.\n"+
			"Offending locations:\n  %s",
			len(offending), strings.Join(offending, "\n  "))
	}

	var stale []string
	for path := range allowlist {
		if !matched[path] {
			stale = append(stale, path)
		}
	}
	slices.Sort(stale)
	if len(stale) > 0 {
		t.Errorf("Found %d allowlisted file(s) that no longer pass an instrumentation.Package value as "+
			"the ext.Component tag. Remove them from the TestNoTypedComponentTag allowlist:\n  %s",
			len(stale), strings.Join(stale, "\n  "))
	}
}

// importName returns the name under which node imports importPath, defaulting to
// defaultName, or "" if node doesn't import it.
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
