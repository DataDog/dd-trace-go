package entrypoint

import (
	"fmt"
	"github.com/dave/dst"
	"go/token"
	"strings"

	"github.com/DataDog/dd-trace-go/internal/tools/process_contribs/internal/codegen"
	"github.com/DataDog/dd-trace-go/internal/tools/process_contribs/internal/typing"
)

type entrypointCreateHooks struct{}

// Apply makes the necessary modifications.
//
// strategy:
// 1. add a new field `disabled bool` in the custom type
// 2. get the value of disabled and store it in the struct
// 3. loop over all the exported methods and find out how to do a no-op when disabled = true
func (e entrypointCreateHooks) Apply(fn *dst.FuncDecl, fCtx FunctionContext, args map[string]string) (map[string]codegen.UpdateNodeFunc, error) {
	s := typing.GetFunctionSignature(fn)
	if len(s.Returns) == 0 || len(s.Returns) > 2 || (len(s.Returns) == 2 && s.Returns[1].Type != "error") {
		return nil, fmt.Errorf("unexpected return type (only know how to handle single result or result/error): %v", s.Returns)
	}

	noopType := "noopHooks"
	_, _, ok := findTypeDeclFile(fCtx.Package, noopType)
	if !ok {
		return nil, fmt.Errorf("type %s needs to be defined for ddtrace:entrypoint:create-hooks", noopType)
	}

	newReturns := []string{"&noopHooks{}"}
	if len(s.Returns) == 2 {
		newReturns = append(newReturns, "nil")
	}

	codegen.RemoveFunctionInjectedBlocks(fn)
	newLines := codegen.EarlyReturnStatement(codegen.IntegrationDisabledCall(), newReturns)
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
