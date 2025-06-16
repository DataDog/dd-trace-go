// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

type funcSpec struct {
	name     string
	receiver string
	params   string
	returns  string
}

func (f funcSpec) String() string {
	var b strings.Builder

	b.WriteString("func ")

	if f.receiver != "" {
		b.WriteString("(")
		b.WriteString(f.receiver)
		b.WriteString(") ")
	}

	b.WriteString(f.name)
	b.WriteString(f.params)

	if f.returns != "" {
		b.WriteString(" ")
		b.WriteString(f.returns)
	}

	return b.String()
}

// extractFromNode inspects the AST of a file and returns its exported functions and types (with methods).
func extractFromNode(node *ast.File) ([]funcSpec, []*typeSpec) {
	var funcs []funcSpec
	// First, collect exported type declarations
	typesMap := make(map[string]*typeSpec)

	for _, decl := range node.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}

		for _, spec := range gen.Specs {
			typSpec := spec.(*ast.TypeSpec)
			if !typSpec.Name.IsExported() {
				continue
			}

			ts := &typeSpec{name: typSpec.Name.Name}
			switch typ := typSpec.Type.(type) {
			case *ast.StructType:
				ts.kind = kindStruct
				ts.fields = extractFromStructType(typ)
				sort.Slice(ts.fields, func(i, j int) bool {
					return ts.fields[i].name < ts.fields[j].name
				})
			case *ast.InterfaceType:
				ts.kind = kindInterface
				ts.methods = extractFromInterfaceType(typ)
				sort.Slice(ts.methods, func(i, j int) bool {
					return ts.methods[i].name < ts.methods[j].name
				})
			default:
				ts.kind = kindAlias
				ts.underlying = formatExpr(typ)
			}

			typesMap[ts.name] = ts
		}
	}
	// Next, collect exported functions and associate methods with types
	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !fn.Name.IsExported() {
			continue
		}

		f := funcSpec{
			name:   fn.Name.Name,
			params: formatFieldList(fn.Type.Params),
		}
		if fn.Type.Results != nil {
			f.returns = formatFieldList(fn.Type.Results)
		}

		if fn.Recv == nil {
			funcs = append(funcs, f)

			continue
		}

		f.receiver = formatReceiver(fn.Recv.List[0].Type)
		typeName := getTypeName(fn.Recv.List[0].Type)

		if ts, ok := typesMap[typeName]; ok {
			ts.methods = append(ts.methods, f)
		}
	}
	// Sort functions by name
	sort.Slice(funcs, func(i, j int) bool {
		return funcs[i].name < funcs[j].name
	})
	// Collect and sort types and their methods
	var foundTypes []*typeSpec

	for _, ts := range typesMap {
		sort.Slice(ts.methods, func(i, j int) bool {
			return ts.methods[i].name < ts.methods[j].name
		})

		foundTypes = append(foundTypes, ts)
	}

	sort.Slice(foundTypes, func(i, j int) bool {
		return foundTypes[i].name < foundTypes[j].name
	})

	return funcs, foundTypes
}

func getTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		switch ident := t.X.(type) {
		case *ast.Ident:
			return ident.Name
		case *ast.IndexListExpr:
			// skip.
		}
	}

	return ""
}

// typeKind represents the kind of type declaration.
type typeKind int

const (
	kindStruct typeKind = iota
	kindInterface
	kindAlias
)

type typeSpec struct {
	name       string
	underlying string // For type definitions.
	fields     []fieldSpec
	methods    []funcSpec
	kind       typeKind
}

func (t typeSpec) String() string {
	var b strings.Builder

	b.WriteString("type ")
	b.WriteString(t.name)

	switch t.kind {
	case kindAlias:
		b.WriteString(" ")
		b.WriteString(t.underlying)

		return b.String()
	case kindStruct:
		if len(t.fields) > 0 {
			b.WriteString(" struct {\n")

			for _, f := range t.fields {
				b.WriteString("\t")
				b.WriteString(f.String())
				b.WriteString("\n")
			}

			b.WriteString("}")
		} else {
			b.WriteString(" struct {}")
		}
	case kindInterface:
		b.WriteString(" interface {\n")

		for _, m := range t.methods {
			b.WriteString("\t")
			b.WriteString(m.String())
			b.WriteString("\n")
		}

		b.WriteString("}")
	}

	return b.String()
}

type fieldSpec struct {
	name string
	typ  string
}

func (f fieldSpec) String() string {
	return fmt.Sprintf("%s %s", f.name, f.typ)
}

func extractFromStructType(structType *ast.StructType) []fieldSpec {
	var fields []fieldSpec

	for _, field := range structType.Fields.List {
		for _, name := range field.Names {
			if !name.IsExported() {
				continue
			}

			fields = append(fields, fieldSpec{
				name: name.Name,
				typ:  formatExpr(field.Type),
			})
		}
	}

	return fields
}

func extractFromInterfaceType(interfaceType *ast.InterfaceType) []funcSpec {
	var methods []funcSpec

	for _, method := range interfaceType.Methods.List {
		if len(method.Names) == 0 {
			continue
		}

		for _, name := range method.Names {
			if !name.IsExported() {
				continue
			}

			f := funcSpec{
				name: name.Name,
			}
			if t, ok := method.Type.(*ast.FuncType); ok {
				f.params = formatFieldList(t.Params)
				if t.Results != nil {
					f.returns = formatFieldList(t.Results)
				}
			}

			methods = append(methods, f)
		}
	}

	return methods
}

const pathSeparator = string(filepath.Separator)

// extractModulePath reads a go.mod file and returns its module path.
func extractModulePath(modFile string) (string, error) {
	modBytes, err := os.ReadFile(modFile)
	if err != nil {
		return "", fmt.Errorf("failed to read go.mod at %s: %w", modFile, err)
	}

	modLines := strings.Split(string(modBytes), "\n")
	for _, line := range modLines {
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}

	return "", fmt.Errorf("no module declaration found in %s", modFile)
}

// findGoMod starts from the given directory and walks up until it finds a go.mod file.
func findGoMod(startDir string) (string, string, error) {
	dir := startDir

	for {
		modPath := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(modPath); err == nil {
			// Found go.mod, extract module path
			modulePath, err := extractModulePath(modPath)
			if err != nil {
				return "", "", err
			}

			return modulePath, dir, nil
		}

		// Go up one directory
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root without finding go.mod
			return "", "", fmt.Errorf("no go.mod found in %s or any parent directory", startDir)
		}

		dir = parent
	}
}

func main() {
	var goModPath string

	flag.StringVar(&goModPath, "gomod", "", "Path to go.mod file (optional, will search in parent directories if not specified)")
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		fmt.Println("Usage: api_extractor [-gomod path/to/go.mod] <path_to_go_module>")
		os.Exit(1)
	}

	dir := args[0]
	if err := run(dir, goModPath); err != nil {
		fmt.Printf("Error extracting public API: %v\n", err)
		os.Exit(1)
	}
}

// formatMethodSignature formats a method with its receiver, name, params, and returns.
func formatMethodSignature(m funcSpec) string {
	var returnPart string
	if m.returns != "" {
		returnPart = " " + m.returns
	}

	return fmt.Sprintf("func (%s) %s%s%s",
		m.receiver,
		m.name,
		m.params,
		returnPart)
}

// collectTypeMethods collects and formats methods for a type.
func collectTypeMethods(t *typeSpec) []string {
	if t.underlying != "" { // Skip for type aliases/definitions
		return nil
	}

	var methods []string
	for _, m := range t.methods {
		if m.receiver != "" {
			methods = append(methods, formatMethodSignature(m))
		}
	}

	return methods
}

func run(dir string, goModPath string) error {
	var allOutput []string

	fset := token.NewFileSet()

	// Find the module information
	var modulePath string

	var err error
	if goModPath != "" {
		// Use specified go.mod file
		modulePath, err = extractModulePath(goModPath)
		if err != nil {
			return fmt.Errorf("failed to find module information: %w", err)
		}
	} else {
		// Find go.mod by walking up directories
		modulePath, _, err = findGoMod(dir)
		if err != nil {
			return fmt.Errorf("failed to find module information: %w", err)
		}
	}

	// Add header once at the beginning.
	allOutput = append(allOutput, "// API Stability Report")
	allOutput = append(allOutput, "// Package: "+filepath.Join(modulePath, dir))
	allOutput = append(allOutput, "// Module: "+modulePath)
	allOutput = append(allOutput, "")

	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		components := strings.Split(path, pathSeparator)
		if slices.Contains(components, "internal") {
			return nil
		}

		node, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		funcs, types := extractFromNode(node)

		// Only process files with exported API.
		if len(funcs) == 0 && len(types) == 0 {
			return nil
		}

		// Add file header comment.
		filePath, err := filepath.Rel(dir, path)
		if err != nil {
			filePath = path
		}

		allOutput = append(allOutput, "// File: "+filePath)
		allOutput = append(allOutput, "")

		// Add functions
		if len(funcs) > 0 {
			allOutput = append(allOutput, "// Package Functions")
			for _, f := range funcs {
				allOutput = append(allOutput, f.String())
			}

			allOutput = append(allOutput, "")
		}

		// Add types and their methods
		if len(types) > 0 {
			allOutput = append(allOutput, "// Types")
			for _, t := range types {
				allOutput = append(allOutput, t.String())
				allOutput = append(allOutput, "")

				// Add methods after their type
				if methods := collectTypeMethods(t); len(methods) > 0 {
					allOutput = append(allOutput, methods...)
					allOutput = append(allOutput, "")
				}
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	for _, line := range allOutput {
		fmt.Println(line)
	}

	return nil
}

func formatFieldList(fields *ast.FieldList) string {
	if fields == nil {
		return "()"
	}

	var params []string

	for _, field := range fields.List {
		typ := formatExpr(field.Type)
		if len(field.Names) == 0 {
			params = append(params, typ)
		} else {
			// Omit parameter names, only include the type
			params = append(params, typ)
		}
	}

	return fmt.Sprintf("(%s)", strings.Join(params, ", "))
}

func formatReceiver(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return "*" + getTypeName(t.X)
	default:
		return getTypeName(expr)
	}
}

func formatExpr(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.BasicLit:
		return t.Value
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + formatExpr(t.X)
	case *ast.SelectorExpr:
		return formatExpr(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		if t.Len == nil {
			return "[]" + formatExpr(t.Elt)
		}

		return fmt.Sprintf("[%s]%s", formatExpr(t.Len), formatExpr(t.Elt))
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.MapType:
		return fmt.Sprintf("map[%s]%s", formatExpr(t.Key), formatExpr(t.Value))
	case *ast.ChanType:
		switch t.Dir {
		case ast.SEND:
			return "chan<- " + formatExpr(t.Value)
		case ast.RECV:
			return "<-chan " + formatExpr(t.Value)
		default:
			return "chan " + formatExpr(t.Value)
		}
	case *ast.FuncType:
		return fmt.Sprintf("func%s%s",
			formatFieldList(t.Params),
			formatFieldList(t.Results))
	case *ast.Ellipsis:
		return "..." + formatExpr(t.Elt)
	default:
		return fmt.Sprintf("%#v", expr)
	}
}
