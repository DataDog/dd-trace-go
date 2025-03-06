package main

import (
	"errors"
	"github.com/dave/dst"
	"log"
)

type entrypointIgnore struct{}

func (e entrypointIgnore) Apply(_ *dst.FuncDecl, _ *dst.Package, _ string, args ...string) (map[string]updateNodeFunc, error) {
	if len(args) == 0 {
		return nil, errors.New("reason cannot be empty")
	}
	reason := args[0]
	log.Printf("ignoring entrypoint function (reason: %s)", reason)
	return nil, nil
}
