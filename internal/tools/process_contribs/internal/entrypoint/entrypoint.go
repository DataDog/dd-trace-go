package entrypoint

import (
	"github.com/DataDog/dd-trace-go/internal/tools/process_contribs/internal/typechecker"
)

type Entrypoint interface {
	Apply(fn typechecker.Function, pkg typechecker.Package, args map[string]string) (typechecker.ApplyFunc, error)
}

var AllEntrypoints = map[string]Entrypoint{
	"wrap":             entrypointWrap{},
	"modify-struct":    entrypointModifyStruct{},
	"create-hooks":     entrypointCreateHooks{},
	"wrap-custom-type": entrypointWrapCustomType{},
	"ignore":           entrypointIgnore{},
	"mirror-package":   entrypointMirrorPackage{},
}
