package entrypoint

import (
	"errors"
	"fmt"
	"github.com/DataDog/dd-trace-go/internal/tools/process_contribs/internal/dsthelpers"
	"github.com/DataDog/dd-trace-go/internal/tools/process_contribs/internal/typechecker"
	"github.com/dave/dst"
	"github.com/dave/dst/dstutil"
)

type entrypointMirrorPackage struct{}

func (e entrypointMirrorPackage) Apply(fn typechecker.Function, pkg typechecker.Package, args map[string]string) (typechecker.ApplyFunc, error) {
	mirroredPkg := args["pkg"]
	if mirroredPkg == "" {
		rawArgs := args["__raw_args"]
		if rawArgs != "" {
			mirroredPkg = rawArgs
		} else {
			return nil, errors.New("package cannot be empty")
		}
	}

	tracedPkg, err := typechecker.LoadExternalPackage(mirroredPkg)
	if err != nil {
		return nil, fmt.Errorf("failed to load package: %w", err)
	}

	tracedPkgFn, ok := tracedPkg.Function(fn.Type.Name())
	if !ok {
		return nil, fmt.Errorf("function %s not found in package %s", fn.Type.Name(), mirroredPkg)
	}

	tracedNumArgs := tracedPkgFn.Type.Signature().Params().Len()
	contribNumArgs := fn.Type.Signature().Params().Len()
	diff := contribNumArgs - tracedNumArgs
	if diff > 1 || diff < -1 {
		return nil, fmt.Errorf("incompatible signatures (ours: %s | theirs: %s)",
			fn.Type.String(),
			tracedPkgFn.Type.String(),
		)
	}

	var fnArgs []string
	for i := 0; i < tracedNumArgs; i++ {
		pkgArg := tracedPkgFn.Type.Signature().Params().At(i)
		arg := fn.Type.Signature().Params().At(i)
		if arg.Type().String() != pkgArg.Type().String() {
			return nil, fmt.Errorf("incompatible signatures (ours: %s | theirs: %s)",
				fn.Type.String(),
				tracedPkgFn.Type.String(),
			)
		}
		fnArgs = append(fnArgs, arg.Name())
	}

	changes := func(cur *dstutil.Cursor) (changed bool, cont bool) {
		if cur.Node() == fn.Node {
			dsthelpers.FunctionRemoveInjectedBlocks(fn.Node)
			newLines := dsthelpers.StatementEarlyReturnFuncCall(dsthelpers.ExpressionIntegrationDisabled(), tracedPkgFn.Type, fnArgs)
			fn.Node.Body.List = append([]dst.Stmt{newLines}, fn.Node.Body.List...)
			return true, false
		}
		return false, true
	}
	return changes, nil
}
