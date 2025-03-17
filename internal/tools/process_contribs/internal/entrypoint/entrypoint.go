package entrypoint

import (
	"github.com/DataDog/dd-trace-go/internal/tools/process_contribs/internal/codegen"
	"github.com/dave/dst"
	"regexp"
)

type FunctionContext struct {
	FilePath    string
	File        *dst.File
	Package     *dst.Package
	AllPackages map[string]*dst.Package
}

type Entrypoint interface {
	Apply(fn *dst.FuncDecl, fCtx FunctionContext, args map[string]string) (map[string]codegen.UpdateNodeFunc, error)
}

var AllEntrypoints = map[string]Entrypoint{
	"wrap":             entrypointWrap{},
	"modify-struct":    entrypointModifyStruct{},
	"create-hooks":     entrypointCreateHooks{},
	"wrap-custom-type": entrypointWrapCustomType{},
	"ignore":           entrypointIgnore{},
	"mirror-package":   entrypointMirrorPackage{},
}

var entrypointCommentRegex = regexp.MustCompile(`^(?:\/\/)?\s*ddtrace:entrypoint:(\S+)(?:\s+(.*))?$`)

type Comment struct {
	Command   string
	Arguments map[string]string
}

func ParseComment(s string) (Comment, bool) {
	// Match the command
	match := entrypointCommentRegex.FindStringSubmatch(s)
	if match == nil {
		return Comment{}, false
	}

	// Extract command and named arguments
	command := match[1]
	namedArgs := parseNamedArgs(match[2])
	namedArgs["__raw_args"] = match[2]

	entry := Comment{
		Command:   command,
		Arguments: namedArgs,
	}
	return entry, true
}

func parseNamedArgs(raw string) map[string]string {
	namedArgs := make(map[string]string)

	var key, value string
	inQuotes := false
	readingValue := false

	for i := 0; i < len(raw); i++ {
		char := raw[i]

		switch {
		case char == '"' && readingValue:
			// Toggle quote state when inside a value
			inQuotes = !inQuotes

		case char == ':' && !readingValue:
			// Found key-value separator
			readingValue = true

		case char == ' ' && !inQuotes:
			// End of a key-value pair (only if not inside quotes)
			if key != "" && value != "" {
				namedArgs[key] = value
			}
			key, value = "", ""
			readingValue = false

		default:
			// Append to key or value
			if readingValue {
				value += string(char)
			} else {
				key += string(char)
			}
		}
	}

	// Store last key-value pair
	if key != "" && value != "" {
		namedArgs[key] = value
	}

	// Remove surrounding quotes from values
	for k, v := range namedArgs {
		if len(v) > 1 && v[0] == '"' && v[len(v)-1] == '"' {
			namedArgs[k] = v[1 : len(v)-1]
		}
	}

	return namedArgs
}
