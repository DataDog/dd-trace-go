package entrypoint

import (
	"fmt"
	"github.com/DataDog/dd-trace-go/v2/tools/process_contribs/internal/dsthelpers"
	"github.com/DataDog/dd-trace-go/v2/tools/process_contribs/internal/typechecker"
	"github.com/dave/dst"
	"github.com/dave/dst/dstutil"
)

type entrypointWrap struct{}

func (e entrypointWrap) Apply(fn typechecker.Function, _ typechecker.Package, _ map[string]string) (typechecker.ApplyFunc, error) {
	s := fn.Type.Signature()

	newReturns := make([]string, s.Results().Len())
	for i := 0; i < s.Results().Len(); i++ {
		ret := s.Results().At(i)

		if ret.Type().String() == "error" {
			newReturns[i] = "nil"
			continue
		}

		found := false
		for j := 0; j < s.Params().Len(); j++ {
			arg := s.Params().At(j)
			if arg.Type().String() == ret.Type().String() {
				if arg.Name() == "" {
					return nil, fmt.Errorf("cannot return %s, argument does not have a name", arg.Type().String())
				}
				newReturns[i] = arg.Name()
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("could not found an argument with the same type as the return field %v (fn: %v)", ret, s)
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
