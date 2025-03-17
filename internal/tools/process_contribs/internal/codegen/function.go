package codegen

import (
	"bytes"
	"fmt"
	"github.com/dave/dst"
	"github.com/dave/dst/decorator"
	"go/parser"
	"go/token"
	"go/types"
	"slices"
	"text/template"
)

func RemoveFunctionInjectedBlocks(fn *dst.FuncDecl) {
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

func EarlyReturnStatement(cond dst.Expr, rets []string) *dst.BlockStmt {
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
			NodeDecs: InjectComments(),
		},
	}
}

func EarlyReturnFuncCall(cond dst.Expr, fn *types.Func, args []string) *dst.BlockStmt {
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
			NodeDecs: InjectComments(),
		},
	}
}

func InjectCommentsBlock(stmt dst.Stmt) *dst.BlockStmt {
	return &dst.BlockStmt{
		List: []dst.Stmt{stmt},
		Decs: dst.BlockStmtDecorations{NodeDecs: InjectComments()},
	}
}

func RawStatement(tmpl string, data map[string]any) dst.Stmt {
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
