package main

import (
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

type funcSpec string

func (f funcSpec) String() string {
	return fmt.Sprintf("func %s", string(f))
}

func extractFromNode(node *ast.File) ([]funcSpec, []*typeSpec) {
	var (
		funcs []funcSpec
		types = make(map[string]*typeSpec)
	)
	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}
			if d.Recv == nil {
				funcs = append(funcs, funcSpec(d.Name.Name))
			} else {
				var typeName string
				switch t := d.Recv.List[0].Type.(type) {
				case *ast.Ident:
					typeName = t.Name
				case *ast.StarExpr:
					switch ident := t.X.(type) {
					case *ast.Ident:
						typeName = ident.Name
					case *ast.IndexListExpr:
						// skip
					}
				}
				if _, ok := types[typeName]; !ok {
					continue
				}
				typeFuncs := types[typeName].funcs
				types[typeName].funcs = append(typeFuncs, funcSpec(d.Name.Name))
			}
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			typ := extractFromGenDecl(d)
			for _, t := range typ {
				types[t.name] = t
			}
		}
	}
	sort.Slice(funcs, func(i, j int) bool {
		return funcs[i] < funcs[j]
	})
	var foundTypes []*typeSpec
	for _, t := range types {
		sort.Slice(t.funcs, func(i, j int) bool {
			return t.funcs[i] < t.funcs[j]
		})
		foundTypes = append(foundTypes, t)
	}
	sort.Slice(foundTypes, func(i, j int) bool {
		return foundTypes[i].name < foundTypes[j].name
	})
	return funcs, foundTypes
}

type typeSpec struct {
	name   string
	fields []fieldSpec
	funcs  []funcSpec
}

func (t typeSpec) String() string {
	var b strings.Builder
	b.WriteString("type ")
	b.WriteString(t.name)
	for _, f := range t.fields {
		b.WriteString("\n  ")
		b.WriteString(f.String())
	}
	for _, f := range t.funcs {
		b.WriteString("\n  ")
		b.WriteString(f.String())
	}
	return b.String()
}

type fieldSpec string

func (f fieldSpec) String() string {
	return fmt.Sprintf("field %s", string(f))
}

func extractFromGenDecl(d *ast.GenDecl) []*typeSpec {
	var types []*typeSpec
	for _, spec := range d.Specs {
		typSpec := spec.(*ast.TypeSpec)
		if !typSpec.Name.IsExported() {
			continue
		}
		ts := &typeSpec{name: typSpec.Name.Name}
		types = append(types, ts)
		switch typ := typSpec.Type.(type) {
		case *ast.StructType:
			ts.fields = extractFromStructType(typ)
			sort.Slice(ts.fields, func(i, j int) bool {
				return ts.fields[i] < ts.fields[j]
			})
		case *ast.InterfaceType:
			ts.funcs = extractFromInterfaceType(typ)
			sort.Slice(ts.funcs, func(i, j int) bool {
				return ts.funcs[i] < ts.funcs[j]
			})
		}
	}
	return types
}

func extractFromStructType(structType *ast.StructType) []fieldSpec {
	var fields []fieldSpec
	for _, field := range structType.Fields.List {
		for _, name := range field.Names {
			if !name.IsExported() {
				continue
			}
			fields = append(fields, fieldSpec(name.Name))
		}
	}
	return fields
}

func extractFromInterfaceType(interfaceType *ast.InterfaceType) []funcSpec {
	var fields []funcSpec
	for _, method := range interfaceType.Methods.List {
		if len(method.Names) == 0 {
			continue
		}
		for _, name := range method.Names {
			if !name.IsExported() {
				continue
			}
			fields = append(fields, funcSpec(name.Name))
		}
	}
	return fields
}

const pathSeparator = string(filepath.Separator)

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
		components := strings.Split(path, pathSeparator)
		if slices.Contains(components, "internal") {
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

		funcs, types := extractFromNode(node)
		// append to apiMap[relPath] the funcs and types' string representations
		for _, f := range funcs {
			apiMap[relPath] = append(apiMap[relPath], f.String())
		}
		for _, t := range types {
			apiMap[relPath] = append(apiMap[relPath], t.String())
		}
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
