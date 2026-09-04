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
	"strconv"
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
    sole %v-family verb in the format and that verb is the final one — the
    verb it actually resolves to, honoring explicit argument indexes like
    %[1]v, not just whichever argument is textually or positionally last. A
    format with an earlier %v (over a non-error value) is still reported,
    even if the last argument happens to be err.Error().
  - The format argument must be a compile-time constant string for
    internalLogPkg's Debug/Info and for every stdLogFuncNames entry —
    constantlogmsg only enforces this for internalLogPkg's Error/Warn, so
    this pass covers the rest itself rather than leave a gap.

This replaces the ruleguard rules in the retired rules/logging_rules.go
(internalLogFormatVerbs, stdLogFormatVerbs, internalLogSuggestErrorString,
internalLogSuggestErrorStringMulti, internalLogVariableFormat,
stdLogVariableFormat).`

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
			// Non-constant format: constantlogmsg's problem for internalLogPkg's
			// Error/Warn (its DefaultFuncs covers exactly those two), but
			// nothing else covers Debug/Info there, or any of stdLogFuncNames —
			// enforce it here for those, restoring what the retired
			// internalLogVariableFormat/stdLogVariableFormat ruleguard rules
			// checked before this package replaced them.
			if r.needsConstantFormatCheck(pkg, fn) && !nolintSuppressed(pass, call.Pos(), "gocritic", "logformatverbs") {
				pass.Reportf(call.Args[0].Pos(), "%s.%s: format argument must be a compile-time constant string; a variable format string breaks dedup and can leak uncontrolled data", pkg, fn)
			}
			return
		}
		finalVerb, vFamilyCount, finalVerbArg := lastVerb(format)
		if vFamilyCount == 0 {
			return // no %v-family verb: nothing for this check
		}

		// The final verb's actual argument, resolved via fmt's own explicit
		// argument-indexing rules — not necessarily call.Args' last element.
		// A format like "%[2]s %[1]v" puts its (only) %v-family verb on
		// argument 1, regardless of which argument is textually or
		// positionally last in the call. call.Args[0] is the format string,
		// so fmt argument n is call.Args[n]; fall back to the plain last
		// argument if the resolved index is somehow out of range (should
		// only happen for a format string invalid enough that fmt itself
		// would fail at runtime).
		lastArg := call.Args[len(call.Args)-1]
		if finalVerbArg > 0 && finalVerbArg < len(call.Args) {
			lastArg = call.Args[finalVerbArg]
		}
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
		case finalVerb != 'v' || vFamilyCount > 1:
			// vFamilyCount > 1 alongside a final %v means at least one OTHER
			// %v-family verb precedes it — necessarily non-final, and
			// forbidden regardless of ITS OWN argument's type (position
			// matters per policy, not just the final argument's type). Report
			// this before considering whether the final argument is an error:
			// suggesting err.Error() for the final verb alone would still
			// leave the earlier verb unflagged, surfacing as a second,
			// different diagnostic on the next run instead of fixing anything.
			pass.Reportf(call.Pos(), "%s.%s: %%v/%%+v/%%#v must be the last format verb; use a specific verb like %%s, %%d, or %%q for earlier arguments", pkg, fn)
		case !lastIsError:
			pass.Reportf(call.Pos(), "%s.%s: %%v/%%+v/%%#v exposes uncontrolled data via reflection; use a specific verb like %%s, %%d, or %%q", pkg, fn)
		default:
			pass.Reportf(call.Pos(), "%s.%s: prefer err.Error() with %%s over %%v for explicit, controlled error formatting", pkg, fn)
		}
	})

	return nil, nil
}

// needsConstantFormatCheck reports whether run must itself enforce a
// constant format string for this (pkg, fn) pair. constantlogmsg's
// DefaultFuncs (internal/telemetry/log/analyzer/analyzer.go) already covers
// internalLogPkg's Error and Warn specifically, since those two also reach
// telemetry's own dedup key — checking them again here would just produce a
// second diagnostic for the same call. Nothing else covers internalLogPkg's
// Debug/Info, or the standard library log package at all: restoring that is
// exactly what the retired internalLogVariableFormat/stdLogVariableFormat
// ruleguard rules did.
func (r *formatVerbsRunner) needsConstantFormatCheck(pkg, fn string) bool {
	switch pkg {
	case r.internalLogPkg:
		return fn == "Debug" || fn == "Info"
	case "log":
		return stdLogFuncNames[fn]
	}
	return false
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

// lastVerb walks format the way fmt's own parser does — skipping %% escapes,
// explicit argument-index brackets ([n], e.g. %[1]v), and any flag/width/
// precision modifiers ([-+# 0], digits, '.', '*') — and reports the final
// verb byte found (0 if none), how many %v-family verbs (i.e. verb == 'v',
// regardless of the +/# flags) appear in total, and which call argument
// (1-based, matching fmt's own "Explicit argument indexes" numbering — so
// call.Args[n] is fmt argument n, since call.Args[0] is the format string
// itself) the final verb actually consumes.
//
// That last part matters because an explicit index can point a verb at an
// argument other than "whichever one comes next": %[2]s %[1]v puts %[1]v
// (a %v-family verb) on argument 1, not on whatever the call's last argument
// happens to be, even though %[1]v is textually the final verb. Tracking
// nextArg (fmt's own running counter, which an explicit index resets for
// every verb after it, per the fmt package doc) is what makes that
// resolution correct instead of assuming positional order. %+v and %#v
// count as the 'v' family; the flags are just modifiers on it.
func lastVerb(format string) (verb byte, vFamilyCount int, finalVerbArg int) {
	i := 0
	nextArg := 1 // fmt's own implicit argument counter, 1-based
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
		argIndex, hasIndex := 0, false
		if ni, n, ok := skipArgIndex(format, i); ok {
			i, argIndex, hasIndex = ni, n, true // %[n]v: explicit index before width/verb
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
			if ni, n, ok := skipArgIndex(format, i); ok {
				i, argIndex, hasIndex = ni, n, true // %.[n]*v: explicit index before precision
			}
			if i < len(format) && format[i] == '*' {
				i++ // precision via argument
			} else {
				for i < len(format) && format[i] >= '0' && format[i] <= '9' {
					i++ // precision
				}
			}
		}
		if ni, n, ok := skipArgIndex(format, i); ok {
			i, argIndex, hasIndex = ni, n, true // %d[n]v-style index right before the verb
		}
		if i >= len(format) {
			break
		}
		verb = format[i]

		resolved := nextArg
		if hasIndex {
			resolved = argIndex
		}
		finalVerbArg = resolved
		nextArg = resolved + 1

		if verb == 'v' {
			vFamilyCount++
		}
		i++
	}
	return verb, vFamilyCount, finalVerbArg
}

// skipArgIndex parses a well-formed %[n] explicit-argument-index bracket
// starting at i, returning the position just past it, the parsed 1-based
// index, and true — or (i, 0, false) if there isn't a well-formed bracket
// there. Go's fmt accepts [n] before a verb, a width, or a precision, to
// select which operand it applies to (see the fmt package doc's "Explicit
// argument indexes" section) — without this, lastVerb would treat the '['
// itself as the verb and silently drop the real one that follows.
func skipArgIndex(format string, i int) (newI, index int, ok bool) {
	if i >= len(format) || format[i] != '[' {
		return i, 0, false
	}
	j := i + 1
	start := j
	for j < len(format) && format[j] >= '0' && format[j] <= '9' {
		j++
	}
	if j == start || j >= len(format) || format[j] != ']' {
		return i, 0, false // not well-formed — leave it; the caller's verb read handles the stray '[' safely
	}
	n, err := strconv.Atoi(format[start:j])
	if err != nil {
		return i, 0, false
	}
	return j + 1, n, true
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
