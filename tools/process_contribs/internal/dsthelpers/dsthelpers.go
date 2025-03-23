package dsthelpers

import (
	"bytes"
	"fmt"
	"github.com/DataDog/dd-trace-go/v2/tools/process_contribs/internal/typechecker"
	"github.com/dave/dst"
	"github.com/dave/dst/decorator"
	"go/parser"
	"go/token"
	"go/types"
	"slices"
	"text/template"
)

const (
	commentStartGenCode = "//ddtrace:gen-entrypoint:start"
	commentEndGenCode   = "//ddtrace:gen-entrypoint:end"
	globalconfigImport  = "gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

func FieldIsInjected(f *dst.Field) bool {
	decs := f.Decorations()
	return slices.Contains(decs.Start.All(), commentStartGenCode)
}

func FieldName(f *dst.Field) string {
	if len(f.Names) == 0 {
		return ""
	}
	return f.Names[0].Name
}

func FieldWithGenCodeDecorations(name, t string) *dst.Field {
	return &dst.Field{
		Names: []*dst.Ident{dst.NewIdent(name)},
		Type:  dst.NewIdent(t),
		Decs: dst.FieldDecorations{
			NodeDecs: genCodeDecorations(),
		},
	}
}

func StructRemoveFields(s *dst.TypeSpec, remove ...string) {
	t := s.Type.(*dst.StructType)
	if t.Fields == nil {
		return
	}

	var keep []*dst.Field
	for _, f := range t.Fields.List {
		name := FieldName(f)
		if slices.Contains(remove, name) {
			continue
		}
		keep = append(keep, f)
	}
	t.Fields.List = keep
}

func StructAddFields(s *dst.TypeSpec, add ...*dst.Field) {
	t := s.Type.(*dst.StructType)
	t.Fields.List = append(t.Fields.List, add...)
}

func FunctionSetReturnName(fn *dst.FuncDecl, pkg typechecker.Package, argIdx int, namePrefix string) string {
	t := fn.Type.Results.List[argIdx]
	if len(t.Names) > 0 {
		// already has a name, nothing to do
		return t.Names[0].Name
	}
	name := pkg.FunctionAvailableIdent(fn, namePrefix)
	for i, ret := range fn.Type.Results.List {
		retName := "_"
		if i == argIdx {
			retName = name
		}
		ret.Names = append(ret.Names, &dst.Ident{Name: retName})
	}
	return name
}

func FunctionSetReceiverName(fn *dst.FuncDecl, name string) string {
	recv := fn.Recv.List[0]
	if len(recv.Names) > 0 {
		return recv.Names[0].Name
	} else {
		recv.Names = append(recv.Names, &dst.Ident{Name: name})
	}
	return name
}

func FunctionRemoveInjectedBlocks(fn *dst.FuncDecl) {
	var keep []dst.Stmt
	for _, stmt := range fn.Body.List {
		decs := stmt.Decorations()
		if slices.Contains(decs.Start.All(), commentStartGenCode) {
			continue
		}
		var keepComments []string
		for _, dec := range decs.Start.All() {
			if dec == commentEndGenCode {
				continue
			}
			keepComments = append(keepComments, dec)
		}
		stmt.Decorations().Start.Replace(keepComments...)
		keep = append(keep, stmt)
	}
	fn.Body.List = keep
}

func StatementFromTemplate(tmpl string, data map[string]any) dst.Stmt {
	t, err := template.New("RawStatement").Parse(tmpl)
	if err != nil {
		panic(err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		panic(err)
	}
	stmt := buf.String()

	// Wrap the statement inside a temporary function
	tempSource := fmt.Sprintf("package temp\nfunc tempFunc() {\n %s \n}", stmt)

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

func StatementWithDecorations(stmt dst.Stmt) *dst.BlockStmt {
	return &dst.BlockStmt{
		List: []dst.Stmt{stmt},
		Decs: dst.BlockStmtDecorations{NodeDecs: genCodeDecorations()},
	}
}

func StatementEarlyReturn(cond dst.Expr, rets []string) *dst.BlockStmt {
	var results []dst.Expr
	for _, ret := range rets {
		results = append(results, &dst.Ident{Name: ret})
	}
	return &dst.BlockStmt{
		List: []dst.Stmt{
			&dst.IfStmt{
				Cond: cond,
				Body: &dst.BlockStmt{
					List: []dst.Stmt{
						&dst.ReturnStmt{
							Results: results,
						},
					},
				},
			},
		},
		Decs: dst.BlockStmtDecorations{
			NodeDecs: genCodeDecorations(),
		},
	}
}

func StatementEarlyReturnFuncCall(cond dst.Expr, fn *types.Func, args []string) *dst.BlockStmt {
	var exprArgs []dst.Expr
	for _, arg := range args {
		exprArgs = append(exprArgs, &dst.Ident{Name: arg})
	}

	call := &dst.CallExpr{
		Fun: &dst.Ident{
			Name: fn.Name(),
			Path: fn.Pkg().Path(),
		},
		Args: exprArgs,
	}

	var bodyStmts []dst.Stmt
	if fn.Signature().Results().Len() == 0 {
		bodyStmts = []dst.Stmt{
			&dst.ExprStmt{X: call},
			&dst.ReturnStmt{},
		}
	} else {
		bodyStmts = []dst.Stmt{
			&dst.ReturnStmt{Results: []dst.Expr{call}},
		}
	}

	return &dst.BlockStmt{
		List: []dst.Stmt{
			&dst.IfStmt{
				Cond: cond,
				Body: &dst.BlockStmt{
					List: bodyStmts,
				},
			},
		},
		Decs: dst.BlockStmtDecorations{
			NodeDecs: genCodeDecorations(),
		},
	}
}

func StatementAssignVariable(name string, expr dst.Expr) *dst.AssignStmt {
	return &dst.AssignStmt{
		Lhs: []dst.Expr{
			&dst.Ident{Name: name},
		},
		Tok: token.DEFINE,
		Rhs: []dst.Expr{
			expr,
		},
	}
}

func StatementWithGenCodeDecorations(statements ...dst.Stmt) *dst.BlockStmt {
	return &dst.BlockStmt{
		List: statements,
		Decs: dst.BlockStmtDecorations{
			NodeDecs: genCodeDecorations(),
		},
	}
}

func ExpressionIntegrationDisabled() *dst.CallExpr {
	return &dst.CallExpr{
		Fun: &dst.Ident{
			Name: "IntegrationDisabled",
			Path: globalconfigImport,
		},
		Args: []dst.Expr{
			&dst.Ident{
				Name: "componentName",
			},
		},
	}
}

func genCodeDecorations() dst.NodeDecs {
	return dst.NodeDecs{
		Start: dst.Decorations{"\n", "// Code generated by process-contrib-entrypoints. DO NOT EDIT.", commentStartGenCode},
		End:   dst.Decorations{"\n", commentEndGenCode},
	}
}
