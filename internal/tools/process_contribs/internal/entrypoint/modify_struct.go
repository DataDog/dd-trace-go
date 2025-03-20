package entrypoint

import (
	"fmt"
	"github.com/DataDog/dd-trace-go/internal/tools/process_contribs/internal/dsthelpers"
	"github.com/DataDog/dd-trace-go/internal/tools/process_contribs/internal/typechecker"
	"github.com/dave/dst"
	"github.com/dave/dst/dstutil"
)

type entrypointModifyStruct struct{}

// Apply just return and do nothing - if return types are strange return and error and check the concrete case
func (e entrypointModifyStruct) Apply(fn typechecker.Function, pkg typechecker.Package, args map[string]string) (typechecker.ApplyFunc, error) {
	s := fn.Type.Signature()

	newReturns := make([]string, s.Results().Len())

	for i := 0; i < s.Results().Len(); i++ {
		ret := s.Results().At(i)
		if ret.Type().String() == "error" {
			newReturns[i] = "nil"
		} else {
			return nil, fmt.Errorf("unexpected return type (only know how to handle error type): %s", ret.Type().String())
		}
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
