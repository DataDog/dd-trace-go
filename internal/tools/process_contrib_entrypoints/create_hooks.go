package main

import (
	"fmt"
	"github.com/dave/dst"
	"github.com/dave/dst/decorator"
	"go/parser"
	"go/token"
	"strings"
)

type entrypointCreateHooks struct{}

// Apply makes the necessary modifications.
//
// strategy:
// 1. add a new field `disabled bool` in the custom type
// 2. get the value of disabled and store it in the struct
// 3. loop over all the exported methods and find out how to do a no-op when disabled = true
func (e entrypointCreateHooks) Apply(fn *dst.FuncDecl, pkg *dst.Package, fPath string, _ ...string) (map[string]updateNodeFunc, error) {
	s := getFunctionSignature(fn)
	if len(s.Returns) == 0 || len(s.Returns) > 2 || (len(s.Returns) == 2 && s.Returns[1].Type != "error") {
		return nil, fmt.Errorf("unexpected return type (only know how to handle single result or result/error): %v", s.Returns)
	}
	
	noopType := "noopHooks"
	_, _, ok := findTypeDeclFile(pkg, noopType)
	if !ok {
		return nil, fmt.Errorf("type %s needs to be defined for ddtrace:entrypoint:create-hooks", noopType)
	}

	newReturns := []string{"&noopHooks{}"}
	if len(s.Returns) == 2 {
		newReturns = append(newReturns, "nil")
	}

	removePreviousInjectedCode(fn)
	newLines := earlyReturnStatement(integrationDisabledCallExpr(), newReturns)
	fn.Body.List = append([]dst.Stmt{newLines}, fn.Body.List...)

	return nil, nil
}

func findTypeDeclFile(pkg *dst.Package, targetType string) (*dst.StructType, string, bool) {
	for fPath, f := range pkg.Files {
		for _, decl := range f.Decls {
			if structType := findStructType(decl, targetType); structType != nil {
				return structType, fPath, true
			}
		}
	}
	return nil, "", false
}

func findStructType(decl dst.Decl, targetType string) *dst.StructType {
	structName := strings.TrimPrefix(targetType, "*")
	genDecl, ok := decl.(*dst.GenDecl)
	if !ok {
		return nil
	}
	if genDecl.Tok != token.TYPE {
		return nil
	}
	for _, spec := range genDecl.Specs {
		if typeSpec, ok := spec.(*dst.TypeSpec); ok {
			if structType, ok := typeSpec.Type.(*dst.StructType); ok {
				if typeSpec.Name.Name == structName {
					return structType
				}
			}
		}
	}

	return nil
}

// 📌 Helper function: Parse a Go statement string and extract dst.Stmt
func parseStmtFromString(stmtStr string) dst.Stmt {
	// Wrap the statement inside a temporary function
	tempSource := fmt.Sprintf("package temp\nfunc tempFunc() { %s }", stmtStr)

	// Parse it into a dst.File
	fset := token.NewFileSet()
	file, err := decorator.ParseFile(fset, "", tempSource, parser.ParseComments)
	if err != nil {
		panic(err)
	}

	// Extract the first statement from the function body
	funcDecl, ok := file.Decls[0].(*dst.FuncDecl)
	if !ok || len(funcDecl.Body.List) == 0 {
		panic("Failed to extract statement from parsed file")
	}

	return funcDecl.Body.List[0]
}
