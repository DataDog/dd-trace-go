package entrypoint

import (
	"fmt"
	"github.com/DataDog/dd-trace-go/internal/tools/process_contribs/internal/codegen"
	"github.com/DataDog/dd-trace-go/internal/tools/process_contribs/internal/typing"
	"github.com/dave/dst"
	"github.com/dave/dst/dstutil"
	"go/token"
	"go/types"
	"golang.org/x/mod/semver"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

type entrypointWrapCustomType struct{}

// strategy:
// 1. add a new field `disabled bool` in the custom type
// 2. get the value of disabled and store it in the struct
// 3. loop over the methods that match from the original argument and the return one
// 4. in all those methods, add the if check and call the original without doing anything if disabled = true
func (e entrypointWrapCustomType) Apply(fn *dst.FuncDecl, fCtx FunctionContext, args map[string]string) (map[string]codegen.UpdateNodeFunc, error) {
	skipMethods := strings.Split(args["skip-methods"], ",")

	s := typing.GetFunctionSignature(fn)
	if len(s.Returns) == 0 || len(s.Returns) > 2 || (len(s.Returns) == 2 && s.Returns[1].Type != "error") {
		return nil, fmt.Errorf("unexpected return type (only know how to handle single result or result/error): %v", s.Returns)
	}
	customType := s.Returns[0].Type

	customTypeDecl, customTypeFile, ok := findTypeDeclFile(fCtx.Package, customType)
	if !ok {
		return nil, fmt.Errorf("could not find type definition for %s", customType)
	}

	// check if a field disabled already exists, without the inject comments
	for _, f := range customTypeDecl.Fields.List {
		if typing.GetFieldName(f) == "disabled" && !typing.IsInjectedField(f) {
			return nil, fmt.Errorf("type declaration %q already has a struct field named \"disabled\"", customType)
		}
	}

	changes := map[string]codegen.UpdateNodeFunc{
		customTypeFile: addDisabledField(customType),
	}

	customTypeReturnName := addNamedReturn(fn, 0, "result")

	codegen.RemoveFunctionInjectedBlocks(fn)

	tmpl := `defer func() {
	if {{.Name}} != nil {
		{{.Name}}.disabled = disabled
	}
}()`
	stmt := codegen.RawStatement(tmpl, map[string]any{"Name": customTypeReturnName})
	newLines := &dst.BlockStmt{
		List: []dst.Stmt{
			&dst.AssignStmt{ // disabled := globalconfig.IntegrationDisabled(componentName)
				Lhs: []dst.Expr{
					&dst.Ident{Name: "disabled"},
				},
				Tok: token.DEFINE,
				Rhs: []dst.Expr{
					codegen.IntegrationDisabledCall(),
				},
			},
			stmt,
		},
		Decs: dst.BlockStmtDecorations{
			NodeDecs: codegen.InjectComments(),
		},
	}

	changes[fCtx.FilePath] = changes[fCtx.FilePath].Chain(func(cur *dstutil.Cursor) (changed bool, cont bool) {
		funcDecl, ok := cur.Node().(*dst.FuncDecl)
		if !ok {
			return false, false
		}
		if fn.Name.Name == funcDecl.Name.Name {
			funcDecl.Body.List = append([]dst.Stmt{newLines}, funcDecl.Body.List...)
			return true, false
		}
		return false, false
	})

	publicMethods := typing.FindPublicMethods(fCtx.Package, customType)

	typePkg, typeName, ok := s.Arguments[0].SplitPackageType()
	if !ok {
		return nil, fmt.Errorf("expected first function argument to be an external type: %s", s.String())
	}
	wrappedTypeName := s.Arguments[0].WithoutFullPath()

	wrappedField := ""
	for _, f := range customTypeDecl.Fields.List {
		if typing.GetExpressionType(f.Type) == wrappedTypeName {
			if len(f.Names) > 0 {
				wrappedField = f.Names[0].Name
			} else {
				wrappedField = typeName
			}
		}
	}
	if wrappedField == "" {
		return nil, fmt.Errorf("could not find wrapped field in type: %s", wrappedTypeName)
	}

	pkgPath, ok := findPackage(typePkg, fCtx.Package.Files[fCtx.FilePath])
	if !ok {
		return nil, fmt.Errorf("could not find package in imports: %s", typePkg)
	}

	originalType, err := typing.LoadExternalType(pkgPath, typeName)
	if err != nil {
		return nil, err
	}

	//methodsByName := map[string]*types.Func{}
	//for i := 0; i < originalType.NumMethods(); i++ {
	//	method := originalType.Method(i)
	//	if method.Exported() {
	//		methodsByName[method.Name()] = method
	//	}
	//}
	methodsByName := getPublicMethods(originalType)

	for f, methods := range publicMethods {
		for _, m := range methods {
			methodName := m.Name.Name
			if slices.Contains(skipMethods, methodName) {
				continue
			}
			_, ok := methodsByName[methodName]
			if !ok {
				return nil, fmt.Errorf("method not found in original type %s: %s", originalType.String(), methodName)
			}
			changes[f] = changes[f].Chain(disabledMethod(customType, methodName, wrappedField))
		}
	}

	return changes, nil
}

func addDisabledField(structType string) codegen.UpdateNodeFunc {
	return func(cur *dstutil.Cursor) (bool, bool) {
		if genDecl, ok := cur.Node().(*dst.GenDecl); ok {
			if t := findStructType(genDecl, structType); t != nil {
				field := &dst.Field{
					Names: []*dst.Ident{dst.NewIdent("disabled")},
					Type:  dst.NewIdent("bool"),
					Decs: dst.FieldDecorations{
						NodeDecs: codegen.InjectComments(),
					},
				}
				codegen.RemoveStructInjectedFields(t, []string{"disabled"})
				codegen.AddStructField(t, field)
				return true, false
			}
			return false, true
		}
		return false, true
	}
}

func addNamedReturn(fn *dst.FuncDecl, argIdx int, prefix string) string {
	customTypeRet := fn.Type.Results.List[argIdx]
	retName := prefix
	if len(customTypeRet.Names) > 0 {
		retName = customTypeRet.Names[0].Name
	} else {
		for i, ret := range fn.Type.Results.List {
			name := typing.GetAvailableVariableName(fn, "result")
			if i == 0 {
				retName = name
			}
			ret.Names = append(ret.Names, &dst.Ident{Name: name})
		}
	}
	return retName
}

func disabledMethod(typeName, methodName, wrappedField string) codegen.UpdateNodeFunc {
	return func(cur *dstutil.Cursor) (bool, bool) {
		// inject stuff in the method
		funcDecl, ok := cur.Node().(*dst.FuncDecl)
		if !ok {
			return false, false
		}
		if funcDecl.Name.Name != methodName {
			return false, false
		}
		if len(funcDecl.Recv.List) == 0 {
			return false, false
		}
		recv := funcDecl.Recv.List[0]
		t := typing.GetExpressionType(recv.Type)
		if t != typeName {
			return false, false
		}

		recvName := ""
		if len(recv.Names) > 0 {
			recvName = recv.Names[0].Name
		} else {
			recvName = typing.GetAvailableVariableName(funcDecl, "r")
			recv.Names = append(recv.Names, &dst.Ident{Name: recvName})
		}

		s := typing.GetFunctionSignature(funcDecl)
		var retLines []string
		if len(s.Returns) > 0 {
			retLines = append(retLines, "return {{.Receiver}}.{{.Original}}.{{.MethodName}}({{.MethodArgs}})")
		} else {
			retLines = append(retLines, "{{.Receiver}}.{{.Original}}.{{.MethodName}}({{.MethodArgs}})", "return")
		}

		code := fmt.Sprintf(`if {{.Receiver}}.disabled {
	%s
}`, strings.Join(retLines, "\n\t"))

		var rets []string
		for _, ret := range s.Arguments {
			rets = append(rets, ret.Name)
		}
		stmt := codegen.RawStatement(code, map[string]any{
			"Receiver":   recvName,
			"Original":   wrappedField,
			"MethodName": methodName,
			"MethodArgs": strings.Join(rets, ", "),
		})
		codegen.RemoveFunctionInjectedBlocks(funcDecl)
		newLines := codegen.InjectCommentsBlock(stmt)
		funcDecl.Body.List = append([]dst.Stmt{newLines}, funcDecl.Body.List...)
		return true, false
	}
}

func findPackage(name string, f *dst.File) (string, bool) {
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if p == name {
			return name, true
		}
	}
	return "", false
}

func isVersion(segment string) bool {
	return semver.IsValid(segment)
}

func getPublicMethods(t *types.Named) map[string]*types.Func {
	methodsByName := map[string]*types.Func{}

	// Get inherited methods from embedded types
	mset := types.NewMethodSet(t)
	for i := 0; i < mset.Len(); i++ {
		method := mset.At(i).Obj().(*types.Func)
		if isExported(method.Name()) {
			methodsByName[method.Name()] = method
		}
	}

	// Get methods declared on *types.Named
	for i := 0; i < t.NumMethods(); i++ {
		method := t.Method(i)
		if isExported(method.Name()) {
			methodsByName[method.Name()] = method
		}
	}

	return methodsByName
}

// isExported checks if a name is public (exported)
func isExported(name string) bool {
	return unicode.IsUpper([]rune(name)[0])
}
