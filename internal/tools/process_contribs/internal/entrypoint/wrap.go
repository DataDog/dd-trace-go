package entrypoint

import (
	"fmt"
	"github.com/DataDog/dd-trace-go/internal/tools/process_contribs/internal/codegen"
	"github.com/DataDog/dd-trace-go/internal/tools/process_contribs/internal/typing"
	"github.com/dave/dst"
)

type entrypointWrap struct{}

func (e entrypointWrap) Apply(fn *dst.FuncDecl, fCtx FunctionContext, args map[string]string) (map[string]codegen.UpdateNodeFunc, error) {
	s := typing.GetFunctionSignature(fn)

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

	codegen.RemoveFunctionInjectedBlocks(fn)
	newLines := codegen.EarlyReturnStatement(codegen.IntegrationDisabledCall(), newReturns)
	fn.Body.List = append([]dst.Stmt{newLines}, fn.Body.List...)
	return nil, nil
}
