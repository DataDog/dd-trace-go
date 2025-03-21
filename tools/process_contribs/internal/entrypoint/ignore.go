package entrypoint

import (
	"errors"
	"github.com/DataDog/dd-trace-go/v2/tools/process_contribs/internal/typechecker"
	"log"
)

type entrypointIgnore struct{}

func (e entrypointIgnore) Apply(fn typechecker.Function, pkg typechecker.Package, args map[string]string) (typechecker.ApplyFunc, error) {
	reason := args["reason"]
	if reason == "" {
		rawArgs := args["__raw_args"]
		if rawArgs != "" {
			reason = rawArgs
		} else {
			return nil, errors.New("reason cannot be empty")
		}
	}
	log.Printf("[package: %s | function: %s | entrypoint: %s] ignoring entrypoint function: %s", pkg.Path(), fn.Type.Name(), "ignore", reason)
	return nil, nil
}
