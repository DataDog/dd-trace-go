package entrypoint

import (
	"fmt"
	"github.com/DataDog/dd-trace-go/v2/tools/process_contribs/internal/dsthelpers"
	"github.com/DataDog/dd-trace-go/v2/tools/process_contribs/internal/typechecker"
	"github.com/dave/dst"
	"github.com/dave/dst/dstutil"
)

type entrypointCreateHooks struct{}

// Apply makes the necessary modifications.
//
// strategy:
// 1. add a new field `disabled bool` in the custom type
// 2. get the value of disabled and store it in the struct
// 3. loop over all the exported methods and find out how to do a no-op when disabled = true
func (e entrypointCreateHooks) Apply(fn typechecker.Function, pkg typechecker.Package, args map[string]string) (typechecker.ApplyFunc, error) {
	s := fn.Type.Signature()

	isIncompatible :=
		s.Results().Len() == 0 ||
			s.Results().Len() > 2 ||
			(s.Results().Len() == 2 && s.Results().At(1).Type().String() != "error")

	if isIncompatible {
		return nil, fmt.Errorf("unexpected return type (only know how to handle single result or result/error): %s", s.Results().String())
	}

	noopType := "noopTracer"
	_, ok := pkg.Struct(noopType)
	if !ok {
		return nil, fmt.Errorf("type %s not found in package", noopType)
	}

	newReturns := []string{"&noopTracer{}"}
	if s.Results().Len() == 2 {
		newReturns = append(newReturns, "nil")
	}

	changes := func(cur *dstutil.Cursor) (changed bool, cont bool) {
		if cur.Node() == fn.Node {
			dsthelpers.FunctionRemoveInjectedBlocks(fn.Node)
			newLines := dsthelpers.StatementEarlyReturn(dsthelpers.ExpressionIntegrationDisabled(), newReturns)
			fn.Node.Body.List = append([]dst.Stmt{newLines}, fn.Node.Body.List...)
			return true, false
		}
		return false, true
	}
	return changes, nil
}
