package main

import (
	"fmt"
	"github.com/dave/dst"
)

type entrypointWrap struct{}

func (e entrypointWrap) Apply(fn *dst.FuncDecl, _ *dst.Package, _ string, _ ...string) (map[string]updateNodeFunc, error) {
	s := getFunctionSignature(fn)

	newReturns := make([]string, len(s.Returns))
	for i, ret := range s.Returns {
		if ret.Type == "error" {
			newReturns[i] = "nil"
			continue
		}
		found := false
		for _, arg := range s.Arguments {
			if arg.Type == ret.Type {
				if arg.Name == "" {
					return nil, fmt.Errorf("cannot return %s, argument does not have a name", arg.Type)
				}
				newReturns[i] = arg.Name
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("could not found an argument with the same type as the return field %v (fn: %v)", ret, s)
		}
	}

	removePreviousInjectedCode(fn)
	newLines := earlyReturnStatement(integrationDisabledCallExpr(), newReturns)
	fn.Body.List = append([]dst.Stmt{newLines}, fn.Body.List...)
	return nil, nil
}
