// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// formatverbs.go replaces the ruleguard rules formerly in
// rules/logging_rules.go (internalLogFormatVerbs, stdLogFormatVerbs, and their
// err.Error() suggestion counterparts), which required golangci-lint's
// gocritic/ruleguard integration.
package analyzer

import (
	"go/ast"
	"go/constant"
	"go/types"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

const formatVerbsDoc = `logformatverbs flags %v, %+v, and %#v usage in internal/log calls (and,
in the small set of files where golangci-lint's depguard config allows it,
the standard library log package): these verbs use reflection and can print
more of a value than intended.

  - %v/%+v/%#v with a non-error-typed last argument is always forbidden.
  - %v/%+v/%#v not in the final format-verb position is always forbidden,
    even for an error-typed last argument.
  - %v/%+v/%#v as the final verb with an error-typed last argument is allowed
    but reported as a suggestion to call err.Error() explicitly instead —
    equally safe, but clearer about intent.
  - err.Error() as the last argument is allowed outright ONLY when it is the
    sole %v-family verb in the format and that verb is the final one. A
    format with an earlier %v (over a non-error value) is still reported,
    even if the last argument happens to be err.Error().

This replaces the ruleguard rules in the retired rules/logging_rules.go
(internalLogFormatVerbs, stdLogFormatVerbs, internalLogSuggestErrorString,
internalLogSuggestErrorStringMulti).`

var internalLogFuncNames = map[string]bool{"Debug": true, "Info": true, "Warn": true, "Error": true}
var stdLogFuncNames = map[string]bool{"Printf": true, "Fatalf": true, "Panicf": true}

// FormatVerbsAnalyzer is the production analyzer, scoped to internal/log and,
// in allow-listed files, the standard library log package.
var FormatVerbsAnalyzer = NewFormatVerbs("github.com/DataDog/dd-trace-go/v2/internal/log")

// NewFormatVerbs returns an analyzer checking internalLogPkg's Debug/Info/Warn/Error
// calls (and, in stdLogAllowedFile files, the standard "log" package's
// Printf/Fatalf/Panicf) for unsafe %v/%+v/%#v usage. Test files are skipped,
// matching the retired ruleguard rules' own scope.
func NewFormatVerbs(internalLogPkg string) *analysis.Analyzer {
	r := &formatVerbsRunner{internalLogPkg: internalLogPkg}
	return &analysis.Analyzer{
		Name:     "logformatverbs",
		Doc:      formatVerbsDoc,
		Requires: []*analysis.Analyzer{inspect.Analyzer},
		Run:      r.run,
	}
}

type formatVerbsRunner struct{ internalLogPkg string }

func (r *formatVerbsRunner) run(pass *analysis.Pass) (any, error) {
	errIface := errorInterface()
	ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	ins.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
		call := n.(*ast.CallExpr)
		if call.Ellipsis.IsValid() || len(call.Args) < 2 {
			return // spread call (f(format, v...)), or no variadic arg to check
		}

		filename := pass.Fset.Position(call.Pos()).Filename
		if strings.HasSuffix(filename, "_test.go") {
			return
		}

		fn, pkg := resolveFunc(pass, call)
		switch {
		case pkg == r.internalLogPkg && internalLogFuncNames[fn]:
		case pkg == "log" && stdLogFuncNames[fn] && stdLogAllowedFile(filename):
		default:
			return
		}

		format, ok := constStringValue(pass, call.Args[0])
		if !ok {
			return // non-constant format: constantlogmsg's problem, not this one
		}
		finalVerb, vFamilyCount := lastVerb(format)
		if vFamilyCount == 0 {
			return // no %v-family verb: nothing for this check
		}

		lastArg := call.Args[len(call.Args)-1]
		lastArgIsErrorDotError := isErrorDotErrorCall(pass, lastArg)
		if lastArgIsErrorDotError && vFamilyCount == 1 && finalVerb == 'v' {
			return // sole %v-family verb, in final position, backed by err.Error()
		}
		if nolintSuppressed(pass, call.Pos(), "gocritic", "logformatverbs") {
			return
		}

		lastType := pass.TypesInfo.TypeOf(lastArg)
		lastIsError := errIface != nil && lastType != nil && types.Implements(lastType, errIface)
		switch {
		case finalVerb != 'v':
			pass.Reportf(call.Pos(), "%s.%s: %%v/%%+v/%%#v must be the last format verb; use a specific verb like %%s, %%d, or %%q for earlier arguments", pkg, fn)
		case !lastIsError:
			pass.Reportf(call.Pos(), "%s.%s: %%v/%%+v/%%#v exposes uncontrolled data via reflection; use a specific verb like %%s, %%d, or %%q", pkg, fn)
		default:
			pass.Reportf(call.Pos(), "%s.%s: prefer err.Error() with %%s over %%v for explicit, controlled error formatting", pkg, fn)
		}
	})

	return nil, nil
}

// stdLogAllowedFile mirrors the file allow-list in .golangci.yml's depguard
// config for the standard "log" package (scripts/, tools/, internal/log/log.go,
// internal/orchestrion/, instrumentation/testutils/sql/sql.go, and test files).
func stdLogAllowedFile(filename string) bool {
	f := filepath.ToSlash(filename)
	switch {
	case strings.Contains(f, "/scripts/"):
		return true
	case strings.Contains(f, "/tools/"):
		return true
	case strings.HasSuffix(f, "/internal/log/log.go"):
		return true
	case strings.Contains(f, "/internal/orchestrion/"):
		return true
	case strings.HasSuffix(f, "/instrumentation/testutils/sql/sql.go"):
		return true
	}
	return false
}

// constStringValue returns the compile-time constant string value of expr,
// or ("", false) if expr is not a constant string.
func constStringValue(pass *analysis.Pass, expr ast.Expr) (string, bool) {
	tv, ok := pass.TypesInfo.Types[expr]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}

// lastVerb walks format the way fmt's own parser does — skipping %% escapes
// and any flag/width/precision modifiers ([-+# 0], digits, '.', '*') — and
// reports the final verb byte found (0 if none) and how many %v-family
// verbs (i.e. verb == 'v', regardless of the +/# flags) appear in total.
// %+v and %#v count as the 'v' family; the flags are just modifiers on it.
func lastVerb(format string) (verb byte, vFamilyCount int) {
	i := 0
	for i < len(format) {
		if format[i] != '%' {
			i++
			continue
		}
		i++ // consume '%'
		if i >= len(format) {
			break
		}
		if format[i] == '%' {
			i++ // %% escape: a literal percent, not a verb
			continue
		}
		for i < len(format) && strings.IndexByte("-+# 0", format[i]) >= 0 {
			i++ // flags
		}
		if i < len(format) && format[i] == '*' {
			i++ // width via argument
		} else {
			for i < len(format) && format[i] >= '0' && format[i] <= '9' {
				i++ // width
			}
		}
		if i < len(format) && format[i] == '.' {
			i++
			if i < len(format) && format[i] == '*' {
				i++ // precision via argument
			} else {
				for i < len(format) && format[i] >= '0' && format[i] <= '9' {
					i++ // precision
				}
			}
		}
		if i >= len(format) {
			break
		}
		verb = format[i]
		if verb == 'v' {
			vFamilyCount++
		}
		i++
	}
	return verb, vFamilyCount
}

// isErrorDotErrorCall reports whether expr is a call to a method literally
// named Error with no arguments (an err.Error() call), regardless of whether
// the receiver's static type is exactly the error interface — this allows
// calling .Error() on concrete error-implementing types too.
func isErrorDotErrorCall(pass *analysis.Pass, expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Error" {
		return false
	}
	recvType := pass.TypesInfo.TypeOf(sel.X)
	errIface := errorInterface()
	return recvType != nil && errIface != nil && types.Implements(recvType, errIface)
}
