package main

import (
	"fmt"
	"github.com/dave/dst"
	"path/filepath"
	"slices"
	"strings"
)

const (
	commentStartEntrypoint = "//ddtrace:gen-entrypoint:start"
	commentEndEntrypoint   = "//ddtrace:gen-entrypoint:end"
)

type variable struct {
	Name string
	Type string
}

func (v variable) String() string {
	res := ""
	if v.Name != "" {
		res += v.Name + " "
	}
	res += v.Type
	return res
}

type functionSignature struct {
	Name      string
	Arguments []variable
	Returns   []variable
}

func (f functionSignature) String() string {
	res := "func"
	if f.Name != "" {
		res = res + " " + f.Name + "("
	}
	var args []string
	for _, arg := range f.Arguments {
		args = append(args, arg.String())
	}
	res = res + strings.Join(args, ", ") + ")"

	var rets []string
	for _, ret := range f.Returns {
		rets = append(rets, ret.String())
	}
	if len(rets) == 0 {
		return res
	}
	if len(rets) == 1 {
		return res + " " + rets[0]
	}
	return res + fmt.Sprintf(" (%s)", strings.Join(rets, ", "))
}

func getFunctionSignature[T *dst.FuncDecl | *dst.FuncType](fn T) functionSignature {
	var (
		name    string
		params  *dst.FieldList
		results *dst.FieldList
	)
	switch t := any(fn).(type) {
	case *dst.FuncDecl:
		name = t.Name.Name
		params = t.Type.Params
		results = t.Type.Results

	case *dst.FuncType:
		name = "" // anonymous function don't have a name
		params = t.Params
		results = t.Results
	}

	var (
		args []variable
		rets []variable
	)
	if params != nil {
		args = parseFields(params.List)
	}
	if results != nil {
		rets = parseFields(results.List)
	}
	return functionSignature{
		Name:      name,
		Arguments: args,
		Returns:   rets,
	}
}

func parseFields(fields []*dst.Field) []variable {
	vars := make([]variable, len(fields))
	for i, ret := range fields {
		name := ""
		if len(ret.Names) > 0 {
			name = ret.Names[0].Name
		}
		vars[i] = variable{
			Name: name,
			Type: getType(ret.Type),
		}
	}
	return vars
}

func getType(varType dst.Expr) string {
	switch t := varType.(type) {
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
			name = getType(elt)
		}
		return "[]" + name

	case *dst.StarExpr:
		return "*" + getType(t.X)

	case *dst.Ident:
		name := t.Name
		if t.Path != "" {
			name = filepath.Base(t.Path) + "." + name
		}
		return name

	case *dst.FuncType:
		return getFunctionSignature(t).String()

	case *dst.InterfaceType:
		return "interface{}"

	case *dst.ArrayType:
		return "[]" + getType(t.Elt)

	case *dst.MapType:
		return fmt.Sprintf("map[%s]%s", getType(t.Key), getType(t.Value))
	}

	panic(fmt.Errorf("don't know how to handle dst.Expr, got type: %T", varType))
}

func removePreviousInjectedCode(fn *dst.FuncDecl) {
	var keep []dst.Stmt
	for _, stmt := range fn.Body.List {
		decs := stmt.Decorations()
		if slices.Contains(decs.Start.All(), commentStartEntrypoint) {
			continue
		}
		var keepComments []string
		for _, dec := range decs.Start.All() {
			if dec == commentEndEntrypoint {
				continue
			}
			keepComments = append(keepComments, dec)
		}
		stmt.Decorations().Start.Replace(keepComments...)
		keep = append(keep, stmt)
	}
	fn.Body.List = keep
}
