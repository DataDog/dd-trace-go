package typing

import (
	"fmt"
	"github.com/dave/dst"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/packages"
	"strings"
)

func GetExpressionType(expr dst.Expr) string {
	switch t := expr.(type) {
	case *dst.SelectorExpr:
		x, ok := t.X.(*dst.Ident)
		if !ok {
			panic(fmt.Errorf("don't know how to handle *dst.SelectorExpr.X, got type: %T", t.X))
		}
		typePkg := x.Name
		typeName := t.Sel.Name
		if typePkg != "" {
			return typePkg + "." + typeName
		}
		return typeName

	case *dst.Ellipsis:
		name := ""
		switch elt := t.Elt.(type) {
		case *dst.Ident:
			name = elt.Name
		default:
			name = GetExpressionType(elt)
		}
		return "[]" + name

	case *dst.StarExpr:
		return "*" + GetExpressionType(t.X)

	case *dst.Ident:
		if t.Obj != nil {
			if assign, ok := t.Obj.Decl.(*dst.AssignStmt); ok {
				if len(assign.Rhs) > 0 {
					return GetExpressionType(assign.Rhs[0])
				}
			}
		}
		name := t.Name
		if t.Path != "" {
			name = t.Path + "." + name
		}
		return name

	case *dst.FuncType:
		return GetFunctionSignature(t).String()

	case *dst.InterfaceType:
		return "interface{}"

	case *dst.ArrayType:
		return "[]" + GetExpressionType(t.Elt)

	case *dst.MapType:
		return fmt.Sprintf("map[%s]%s", GetExpressionType(t.Key), GetExpressionType(t.Value))

	case *dst.UnaryExpr:
		name := ""
		if t.Op == token.AND {
			name = "*"
		}
		return name + GetExpressionType(t.X)

	case *dst.CompositeLit:
		return GetExpressionType(t.Type)

	case *dst.ChanType:
		prefix := "chan"
		switch t.Dir {
		case dst.SEND:
			prefix = prefix + "<-"
		case dst.RECV:
			prefix = "<-" + prefix
		}

		return prefix + " " + GetExpressionType(t.Value)
	}

	panic(fmt.Errorf("don't know how to handle dst.Expr, got type: %T", expr))
}

func LoadExternalType(pkg string, typeName string) (*types.Named, error) {
	cfg := &packages.Config{
		Mode: packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports, // Load type info
	}

	pkgs, err := packages.Load(cfg, pkg)
	if err != nil || len(pkgs) == 0 {
		return nil, err
	}

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

func LoadExternalPublicFunctions(pkg string) ([]*types.Func, error) {
	cfg := &packages.Config{
		Mode: packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports, // Load type info
	}

	pkgs, err := packages.Load(cfg, pkg)
	if err != nil || len(pkgs) == 0 {
		return nil, err
	}

	var publicFuncs []*types.Func
	scope := pkgs[0].Types.Scope()

	for _, name := range scope.Names() {
		obj := scope.Lookup(name)

		// Check if the object is a function (but NOT a method)
		if fn, ok := obj.(*types.Func); ok && fn.Type() != nil && isPublic(fn.Name()) {
			if fn.Signature().Recv() == nil {
				publicFuncs = append(publicFuncs, fn)
			}
		}
	}

	return publicFuncs, nil
}

func isPublic(name string) bool {
	return name != "" && strings.ToUpper(name[:1]) == name[:1]
}
