package main

import "github.com/dave/dst"

type entrypointModifyStruct struct{}

// just return and do nothing - if return types are strange fail this script and check the concrete case
func (e entrypointModifyStruct) Apply(fn *dst.FuncDecl, _ ...string) error {
	return nil
}
