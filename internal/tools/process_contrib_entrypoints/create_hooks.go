package main

import "github.com/dave/dst"

type entrypointCreateHooks struct{}

// strategy:
// 1. add a new field `disabled bool` in the custom type
// 2. get the value of disabled and store it in the struct
// 3. loop over all the exported methods and find out how to do a no-op when disabled = true
func (e entrypointCreateHooks) Apply(fn *dst.FuncDecl, _ ...string) error {
	return nil
}
