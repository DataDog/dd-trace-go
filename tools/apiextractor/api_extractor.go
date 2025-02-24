package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func extractPublicAPI(dir string) ([]string, error) {
	var api []string
	fset := token.NewFileSet()

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		node, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		api = append(api, extractFromNode(node)...)
		return nil
	})

	return api, err
}

func extractFromNode(node *ast.File) []string {
	var api []string
	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}
			api = append(api, fmt.Sprintf("func %s", d.Name.Name))
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			api = append(api, extractFromGenDecl(d)...)
		}
	}
	return api
}

func extractFromGenDecl(d *ast.GenDecl) []string {
	var api []string
	for _, spec := range d.Specs {
		typeSpec := spec.(*ast.TypeSpec)
		if !typeSpec.Name.IsExported() {
			continue
		}
		api = append(api, fmt.Sprintf("type %s", typeSpec.Name.Name))
		if structType, ok := typeSpec.Type.(*ast.StructType); ok {
			api = append(api, extractFromStructType(structType)...)
		}
	}
	return api
}

func extractFromStructType(structType *ast.StructType) []string {
	var api []string
	for _, field := range structType.Fields.List {
		for _, name := range field.Names {
			if !name.IsExported() {
				continue
			}
			api = append(api, fmt.Sprintf("  field %s", name.Name))
		}
	}
	return api
}

func run(dir string) error {
	var apiMap = make(map[string][]string)
	fset := token.NewFileSet()

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		node, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(dir, filepath.Dir(path))
		if err != nil {
			return err
		}

		apiMap[relPath] = append(apiMap[relPath], extractFromNode(node)...)
		return nil
	})

	if err != nil {
		return err
	}

	var keys []string
	for k := range apiMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, pkg := range keys {
		fmt.Printf("Package: %s\n", pkg)
		for _, entry := range apiMap[pkg] {
			fmt.Println(entry)
		}
		fmt.Println()
	}
	return nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: api_extractor <path_to_go_module>")
		os.Exit(1)
	}

	dir := os.Args[1]
	if err := run(dir); err != nil {
		fmt.Printf("Error extracting public API: %v\n", err)
		os.Exit(1)
	}
}
