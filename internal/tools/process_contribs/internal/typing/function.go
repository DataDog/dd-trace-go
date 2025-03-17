package typing

import (
	"fmt"
	"github.com/dave/dst"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

type Variable struct {
	Name string
	Type string
}

func (v Variable) String() string {
	res := ""
	if v.Name != "" {
		res += v.Name + " "
	}
	res += v.Type
	return res
}

func (v Variable) SplitPackageType() (string, string, bool) {
	lastDot := strings.LastIndex(v.Type, ".")

	if lastDot == -1 {
		return "", "", false
	}

	pkg := v.Type[:lastDot]
	typeName := v.Type[lastDot+1:]

	return strings.TrimPrefix(pkg, "*"), typeName, true
}

func (v Variable) WithoutFullPath() string {
	pkg, name, ok := v.SplitPackageType()
	if !ok {
		return v.String()
	}
	res := ""
	if strings.HasPrefix(v.Type, "*") {
		res += "*"
	}
	base := filepath.Base(pkg)
	// TODO: missing last part of path
	return res + base + "." + name
}

type FunctionSignature struct {
	Name      string
	Arguments []Variable
	Returns   []Variable
}

func (f FunctionSignature) String() string {
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

func GetFunctionSignature[T *dst.FuncDecl | *dst.FuncType](fn T) FunctionSignature {
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
		args []Variable
		rets []Variable
	)
	if params != nil {
		args = parseFields(params.List)
	}
	if results != nil {
		rets = parseFields(results.List)
	}
	return FunctionSignature{
		Name:      name,
		Arguments: args,
		Returns:   rets,
	}
}

func parseFields(fields []*dst.Field) []Variable {
	vars := make([]Variable, 0)
	for _, f := range fields {
		t := GetExpressionType(f.Type)
		if len(f.Names) > 0 {
			for _, n := range f.Names {
				vars = append(vars, Variable{
					Name: n.Name,
					Type: t,
				})
			}
		} else {
			vars = append(vars, Variable{
				Name: "",
				Type: t,
			})
		}
	}
	return vars
}

func GetAvailableVariableName(fn *dst.FuncDecl, prefix string) string {
	tryName := prefix
	count := 1
	for isIdentifierUsed(fn, tryName) {
		tryName = prefix + strconv.Itoa(count)
		count++
	}
	return tryName
}

func isIdentifierUsed(fn *dst.FuncDecl, targetIdent string) bool {
	s := GetFunctionSignature(fn)
	for _, arg := range s.Arguments {
		if arg.Name == targetIdent {
			return true
		}
	}
	for _, ret := range s.Returns {
		if ret.Name == targetIdent {
			return true
		}
	}
	for _, stmt := range fn.Body.List {
		if assign, ok := stmt.(*dst.AssignStmt); ok {
			for _, l := range assign.Lhs {
				if ident, ok := l.(*dst.Ident); ok {
					if ident.Name == targetIdent {
						return true
					}
				}
			}
		}
	}
	return false
}

func FindPublicMethods(pkg *dst.Package, typeName string) map[string][]*dst.FuncDecl {
	res := make(map[string][]*dst.FuncDecl)
	for fPath, f := range pkg.Files {
		for _, decl := range f.Decls {
			if fn, ok := decl.(*dst.FuncDecl); ok {
				if !IsPublicFunction(fn) {
					continue
				}
				if fn.Recv != nil && len(fn.Recv.List) > 0 {
					t := GetExpressionType(fn.Recv.List[0].Type)
					if strings.TrimPrefix(t, "*") == strings.TrimPrefix(typeName, "*") {
						res[fPath] = append(res[fPath], fn)
					}
				}
			}
		}
	}
	return res
}

func IsPublicFunction(fn *dst.FuncDecl) bool {
	return unicode.IsUpper(rune(fn.Name.Name[0]))
}
