package entrypoint

import (
	"fmt"
	"github.com/DataDog/dd-trace-go/internal/tools/process_contribs/internal/codegen"
	"github.com/DataDog/dd-trace-go/internal/tools/process_contribs/internal/typing"
	"github.com/dave/dst"
)

type entrypointModifyStruct struct{}

// just return and do nothing - if return types are strange fail this script and check the concrete case
func (e entrypointModifyStruct) Apply(fn *dst.FuncDecl, fCtx FunctionContext, args map[string]string) (map[string]codegen.UpdateNodeFunc, error) {
	s := typing.GetFunctionSignature(fn)
	newReturns := make([]string, len(s.Returns))
	for i, ret := range s.Returns {
		if ret.Type == "error" {
			newReturns[i] = "nil"
		} else {
			return nil, fmt.Errorf("unexpected return type (only know how to handle error type): %s", ret.Type)
		}
	}
	codegen.RemoveFunctionInjectedBlocks(fn)
	newLines := codegen.EarlyReturnStatement(codegen.IntegrationDisabledCall(), newReturns)
	fn.Body.List = append([]dst.Stmt{newLines}, fn.Body.List...)
	return nil, nil
}
