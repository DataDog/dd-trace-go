package main

import (
	"fmt"
	"github.com/dave/dst"
	"github.com/dave/dst/dstutil"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/packages"
)

type entrypointWrapCustomType struct{}

// strategy:
// 1. add a new field `disabled bool` in the custom type
// 2. get the value of disabled and store it in the struct
// 3. loop over the methods that match from the original argument and the return one
// 4. in all those methods, add the if check and call the original without doing anything if disabled = true
func (e entrypointWrapCustomType) Apply(fn *dst.FuncDecl, pkg *dst.Package, _ string, _ ...string) (map[string]updateNodeFunc, error) {
	s := getFunctionSignature(fn)
	if len(s.Returns) == 0 || len(s.Returns) > 2 || (len(s.Returns) == 2 && s.Returns[1].Type != "error") {
		return nil, fmt.Errorf("unexpected return type (only know how to handle single result or result/error): %v", s.Returns)
	}
	customType := s.Returns[0].Type

	typeDecl, typeFile, ok := findTypeDeclFile(pkg, customType)
	if !ok {
		return nil, fmt.Errorf("could not find type definition for %s", customType)
	}

	// check if a field disabled already exists, without the inject comments
	for _, f := range typeDecl.Fields.List {
		if fieldName(f) == "disabled" && !isInjectedField(f) {
			return nil, fmt.Errorf("type declaration %q already has a struct field named \"disabled\"", customType)
		}
	}

	addDisabledField := func(cur *dstutil.Cursor) (bool, bool) {
		if genDecl, ok := cur.Node().(*dst.GenDecl); ok {
			if structType := findStructType(genDecl, customType); structType != nil {
				field := &dst.Field{
					Names: []*dst.Ident{dst.NewIdent("disabled")},
					Type:  dst.NewIdent("bool"),
					Decs: dst.FieldDecorations{
						NodeDecs: injectComments,
					},
				}
				removePreviousInjectStructFields(structType, []string{"disabled"})
				addStructField(structType, field)
				return true, false
			}
			return false, true
		}
		return false, true
	}

	extraChanges := map[string]updateNodeFunc{
		typeFile: addDisabledField,
	}

	customTypeRet := fn.Type.Results.List[0]
	retName := "result"
	if len(customTypeRet.Names) > 0 {
		retName = customTypeRet.Names[0].Name
	} else {
		for i, ret := range fn.Type.Results.List {
			name := getUnusedIdentifier(fn, "result")
			if i == 0 {
				retName = name
			}
			ret.Names = append(ret.Names, &dst.Ident{Name: name})
		}
	}

	removePreviousInjectedCode(fn)

	rawCode := fmt.Sprintf(`defer func() {
	if %s != nil {
		%s.disabled = disabled
	}
}()`, retName, retName)

	stmt := parseStmtFromString(rawCode)

	newLines := &dst.BlockStmt{
		List: []dst.Stmt{
			&dst.AssignStmt{ // disabled := globalconfig.IntegrationDisabled(componentName)
				Lhs: []dst.Expr{
					&dst.Ident{Name: "disabled"},
				},
				Tok: token.DEFINE,
				Rhs: []dst.Expr{
					integrationDisabledCallExpr(),
				},
			},
			stmt,
		},
		Decs: dst.BlockStmtDecorations{
			NodeDecs: injectComments,
		},
	}
	fn.Body.List = append([]dst.Stmt{newLines}, fn.Body.List...)

	publicMethods := findPublicMethods(pkg, customType)

	typePkg, typeName, ok := s.Arguments[0].SplitPackageType()
	if !ok {
		return nil, fmt.Errorf("expected first function argument to be an external type: %s", s.String())
	}

	originalType, err := loadExternalType(typePkg, typeName)
	if err != nil {
		return nil, err
	}

	methodsByName := map[string]*types.Func{}
	for i := 0; i < originalType.NumMethods(); i++ {
		method := originalType.Method(i)
		if method.Exported() {
			methodsByName[method.Name()] = method
		}
	}

	for _, methods := range publicMethods {
		for _, m := range methods {
			_, ok := methodsByName[m.Name.Name]
			if !ok {
				// for these, we should force the user to check manually, and add a comment when its resolved.
				return nil, fmt.Errorf("method not found in original type: %s", m.Name.Name)
			}
		}
	}

	return extraChanges, nil
}

func loadExternalType(pkg string, typeName string) (*types.Named, error) {
	// 📌 Load the package using go/packages
	cfg := &packages.Config{
		Mode: packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports, // Load type info
	}

	pkgs, err := packages.Load(cfg, pkg)
	if err != nil || len(pkgs) == 0 {
		return nil, err
	}

	// 📌 Find the struct type "Client"
	var findType *types.Named
	scope := pkgs[0].Types.Scope()

	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if obj, ok := obj.(*types.TypeName); ok {
			if named, ok := obj.Type().(*types.Named); ok {
				if named.Obj().Name() == typeName {
					findType = named
					break
				}
			}
		}
	}

	if findType == nil {
		return nil, fmt.Errorf("%q type not found in package: %q", typeName, pkg)
	}
	return findType, nil
}
