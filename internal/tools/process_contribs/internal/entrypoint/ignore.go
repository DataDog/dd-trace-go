package entrypoint

import (
	"errors"
	"github.com/DataDog/dd-trace-go/internal/tools/process_contribs/internal/codegen"
	"github.com/dave/dst"
	"log"
)

type entrypointIgnore struct{}

func (e entrypointIgnore) Apply(fn *dst.FuncDecl, fCtx FunctionContext, args map[string]string) (map[string]codegen.UpdateNodeFunc, error) {
	reason := args["reason"]
	if reason == "" {
		rawArgs := args["__raw_args"]
		if rawArgs != "" {
			reason = rawArgs
		} else {
			return nil, errors.New("reason cannot be empty")
		}
	}
	log.Printf("ignoring entrypoint function (reason: %s)", reason)
	return nil, nil
}
