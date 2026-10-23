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
			"which provides service name source information for better precedence.\n"+
			"Offending locations:\n  %s",
			len(offending), strings.Join(offending, "\n  "))
	}
}

// legacyNamingPackages lists every Package key in packages.go that still carries a
// naming field (the legacy DD_TRACE_SPAN_ATTRIBUTE_SCHEMA table). It is the
// closed set grandfathered in before contrib/INTEGRATIONS.md started directing new
// integrations away from naming: new entries must use the global DD_SERVICE and a
// hardcoded operation name literal instead, as the comment on PackageInfo.naming
// explains. See TestNoNewNamingMapEntries.
var legacyNamingPackages = map[Package]bool{
	Package99DesignsGQLGen:           true,
	PackageAWSDatadogLambdaGo:        true,
	PackageAWSSDKGo:                  true,
	PackageAWSSDKGoV2:                true,
	PackageAerospikeClientGoV7:       true,
	PackageBradfitzGoMemcache:        true,
	PackageChi:                       true,
	PackageChiV5:                     true,
	PackageConfluentKafkaGo:          true,
	PackageConfluentKafkaGoV2:        true,
	PackageDatabaseSQL:               true,
	PackageDimfeldHTTPTreeMuxV5:      true,
	PackageEmickleiGoRestful:         true,
	PackageEmickleiGoRestfulV3:       true,
	PackageGCPPubsub:                 true,
	PackageGCPPubsubV2:               true,
	PackageGRPC:                      true,
	PackageGaryburdRedigo:            true,
	PackageGin:                       true,
	PackageGlobalsignMgo:             true,
	PackageGoCQL:                     true,
	PackageGoElasticSearchV6:         true,
	PackageGoFiberV2:                 true,
	PackageGoPGV10:                   true,
	PackageGoRedis:                   true,
	PackageGoRedisV7:                 true,
	PackageGoRedisV8:                 true,
	PackageGojiV1Web:                 true,
	PackageGoogleAPI:                 true,
	PackageGorillaMux:                true,
	PackageGormIOGormV1:              true,
	PackageGraphGophersGraphQLGo:     true,
	PackageGraphQLGoGraphQL:          true,
	PackageHashicorpConsulAPI:        true,
	PackageHashicorpVaultAPI:         true,
	PackageIBMSarama:                 true,
	PackageJackcPGXV5:                true,
	PackageJulienschmidtHTTPRouter:   true,
	PackageLabstackEcho:              true,
	PackageLabstackEchoV4:            true,
	PackageLabstackEchoV5:            true,
	PackageMark3LabsMCPGo:            true,
	PackageModelContextProtocolGoSDK: true,
	PackageMongoDriver:               true,
	PackageMongoDriverV2:             true,
	PackageNetHTTP:                   true,
	PackageOlivereElasticV5:          true,
	PackageRedigo:                    true,
	PackageRedisGoRedisV9:            true,
	PackageRedisRueidis:              true,
	PackageSegmentioKafkaGo:          true,
	PackageShopifySarama:             true,
	PackageSyndtrGoLevelDB:           true,
	PackageTidwallBuntDB:             true,
	PackageTwitchTVTwirp:             true,
	PackageTwmbFranzGo:               true,
	PackageUptraceBun:                true,
	PackageUrfaveNegroni:             true,
	PackageValkeyIoValkeyGo:          true,
	PackageValyalaFastHTTP:           true,
}

// TestNoNewNamingMapEntries guards the recurring review feedback from PR #3280
// (opensearch-go.v4) and PR #5296 (go-kratos/kratos.v3): a new integration's
// entry in instrumentation/packages.go should not set the legacy naming field.
// New integrations use the global DD_SERVICE (instr.ServiceName with no naming
// entry) and a hardcoded operation name literal, per contrib/INTEGRATIONS.md
// section 5 ("Service name" / "Operation name"). naming is the
// DD_TRACE_SPAN_ATTRIBUTE_SCHEMA table kept only for packages that had it
// before that convention existed; legacyNamingPackages is that closed,
// enumerated set. Any package key with a non-nil naming map that is not in
// legacyNamingPackages is a new integration repeating the reviewed mistake.
func TestNoNewNamingMapEntries(t *testing.T) {
	packagesMu.RLock()
	defer packagesMu.RUnlock()

	var offending []string
	for pkg, info := range packages {
		if info.naming == nil {
			continue
		}
		if legacyNamingPackages[pkg] {
			continue
		}
		offending = append(offending, string(pkg))
	}
	slices.Sort(offending)

	if len(offending) > 0 {
		t.Errorf("Found %d package(s) in instrumentation/packages.go with a new naming map entry: %s.\n"+
			"New integrations must not set PackageInfo.naming: use the global DD_SERVICE "+
			"(instr.ServiceName(instrumentation.ComponentDefault, nil), no naming entry) and a "+
			"hardcoded operation name string literal instead. See contrib/INTEGRATIONS.md section 5 "+
			"(\"Service name\" / \"Operation name\") and the naming field's doc comment on PackageInfo.\n"+
			"If this package genuinely needs the legacy per-component naming-schema table, add it to "+
			"legacyNamingPackages in instrumentation/contrib_test.go with a comment explaining why.",
			len(offending), strings.Join(offending, ", "))
	}
}
