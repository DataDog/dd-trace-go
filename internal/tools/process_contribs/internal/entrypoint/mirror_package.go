package entrypoint

import (
	"errors"
	"fmt"
	"github.com/DataDog/dd-trace-go/internal/tools/process_contribs/internal/codegen"
	"github.com/DataDog/dd-trace-go/internal/tools/process_contribs/internal/typing"
	"github.com/dave/dst"
	"go/types"
)

type entrypointMirrorPackage struct{}

func (e entrypointMirrorPackage) Apply(fn *dst.FuncDecl, fCtx FunctionContext, args map[string]string) (map[string]codegen.UpdateNodeFunc, error) {
	pkg := args["pkg"]
	if pkg == "" {
		rawArgs := args["__raw_args"]
		if rawArgs != "" {
			pkg = rawArgs
		} else {
			return nil, errors.New("package cannot be empty")
		}
	}

	pkgFunctions, err := typing.LoadExternalPublicFunctions(pkg)
	if err != nil {
		return nil, err
	}

	var found *types.Func
	for _, pkgFn := range pkgFunctions {
		if pkgFn.Name() == fn.Name.Name {
			found = pkgFn
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("function %s not found in package %s", fn.Name.Name, pkg)
	}

	s := typing.GetFunctionSignature(fn)

	numArgs := found.Signature().Params().Len()
	diff := len(s.Arguments) - numArgs
	if diff > 1 || diff < -1 {
		return nil, fmt.Errorf("unexpected number of arguments for %s (want: %d, got: %d)",
			found.String(),
			numArgs,
			len(s.Arguments))
	}

	var fnArgs []string
	for i := 0; i < numArgs; i++ {
		pkgArg := found.Signature().Params().At(i)
		arg := s.Arguments[i]
		if arg.Type != pkgArg.Type().String() {
			return nil, fmt.Errorf("argument types don't match for %s.%s argument %d (want: %s, got: %s)",
				found.Pkg().Path(),
				found.Name(),
				i+1,
				pkgArg.Type().String(),
				arg.Type,
			)
		}
		fnArgs = append(fnArgs, arg.Name)
	}

	codegen.RemoveFunctionInjectedBlocks(fn)
	newLines := codegen.EarlyReturnFuncCall(codegen.IntegrationDisabledCall(), found, fnArgs)
	fn.Body.List = append([]dst.Stmt{newLines}, fn.Body.List...)
	return nil, nil
}
