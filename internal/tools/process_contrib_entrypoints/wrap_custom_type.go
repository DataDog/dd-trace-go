package main

import "github.com/dave/dst"

type entrypointWrapCustomType struct{}

// strategy:
// 1. add a new field `disabled bool` in the custom type
// 2. get the value of disabled and store it in the struct
// 3. loop over the methods that match from the original argument and the return one
// 4. in all those methods, add the if check and call the original without doing anything if disabled = true
func (e entrypointWrapCustomType) Apply(fn *dst.FuncDecl, _ ...string) error {
	return nil
}
