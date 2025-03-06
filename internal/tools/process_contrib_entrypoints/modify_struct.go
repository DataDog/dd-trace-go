package main

import (
	"fmt"
	"github.com/dave/dst"
)

type entrypointModifyStruct struct{}

// just return and do nothing - if return types are strange fail this script and check the concrete case
func (e entrypointModifyStruct) Apply(fn *dst.FuncDecl, _ *dst.Package, _ string, _ ...string) (map[string]updateNodeFunc, error) {
	s := getFunctionSignature(fn)
	newReturns := make([]string, len(s.Returns))
	for i, ret := range s.Returns {
		if ret.Type == "error" {
			newReturns[i] = "nil"
		} else {
			return nil, fmt.Errorf("unexpected return type (only know how to handle error type): %s", ret.Type)
		}
	}
	removePreviousInjectedCode(fn)
	newLines := earlyReturnStatement(integrationDisabledCallExpr(), newReturns)
	fn.Body.List = append([]dst.Stmt{newLines}, fn.Body.List...)
	return nil, nil
}
