// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"go/ast"
	"go/token"
	"slices"
	"strings"
)

// errtrackIgnore is a standalone directive, not a //nolint: entry: this tool
// is the repository's own scanner, not a golangci-lint linter, so naming it in
// a //nolint: comment makes golangci-lint's nolint filter warn about an
// unknown linter on every run. Mirrors //configaudit:ignore.
const errtrackIgnore = "errtrack:ignore"

// hasIgnoreDirective reports whether text is a comment carrying
// //errtrack:ignore, with or without a trailing reason
// (e.g. "//errtrack:ignore adopted in #5251"). Both line and block comment
// forms are recognized, like golangci-lint's nolint, so a /* */ directive
// suppresses the same as a // one.
func hasIgnoreDirective(text string) bool {
	t := strings.TrimRight(strings.TrimLeft(text, "/*"), "*/ ")
	fields := strings.Fields(t)
	return len(fields) > 0 && fields[0] == errtrackIgnore
}

// ignoreDirectiveLines returns, for each 1-based line in file that carries an
// //errtrack:ignore directive, the sorted columns of that line's directive
// comments.
//
// Directive semantics (exact): a directive suppresses a call when the comment
// sits on a line spanned by the call itself — a trailing comment on the
// call's first line, or a comment anywhere between a multi-line call's opening
// and closing parenthesis. A standalone comment line above the call does NOT
// suppress it: there is no reliable way to bind a preceding comment to the
// next statement, and a broad "comment near the call" rule would silently
// swallow directives meant for other lines. When multiple calls share a
// line, a trailing comment's column (not just its line) decides which call
// it suppresses; see applyIgnoreDirectives.
func ignoreDirectiveLines(file *ast.File, fset *token.FileSet) map[int][]int {
	out := map[int][]int{}
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			if hasIgnoreDirective(c.Text) {
				pos := fset.Position(c.Pos())
				out[pos.Line] = append(out[pos.Line], pos.Column)
			}
		}
	}
	for line := range out {
		slices.Sort(out[line])
	}
	return out
}

// applyIgnoreDirectives sets Ignored on each site in sites, mutating them in
// place, from the directive comment columns collected by
// ignoreDirectiveLines. Each binding rule lives in its own helper:
// applySpannedDirectives for multi-line calls, applySameLineDirectives for
// single-line calls.
func applyIgnoreDirectives(sites []Site, suppressed map[int][]int) {
	for i := range sites {
		if sites[i].Line != sites[i].endLine {
			applySpannedDirectives(&sites[i], suppressed)
		}
	}
	for line, idxs := range sameLineCalls(sites) {
		applySameLineDirectives(sites, idxs, suppressed[line])
	}
}

// applySpannedDirectives carries the multi-line rule: a call spanning more
// than one line is suppressed by any directive on a line it spans.
func applySpannedDirectives(site *Site, suppressed map[int][]int) {
	for line := site.Line; line <= site.endLine; line++ {
		if len(suppressed[line]) > 0 {
			site.Ignored = true
			return
		}
	}
}

// sameLineCalls groups, by line, the indices of the single-line calls in
// sites.
func sameLineCalls(sites []Site) map[int][]int {
	sameLine := map[int][]int{}
	for i := range sites {
		if sites[i].Line == sites[i].endLine {
			sameLine[sites[i].Line] = append(sameLine[sites[i].Line], i)
		}
	}
	return sameLine
}

// applySameLineDirectives carries the single-line rule: a directive at column
// c suppresses the nearest call whose own column is at or before c, so
// "log.Error(a); log.Warn(b) //errtrack:ignore" binds the directive to Warn,
// not to both calls on the line.
func applySameLineDirectives(sites []Site, idxs []int, cols []int) {
	if len(cols) == 0 {
		return
	}
	slices.SortFunc(idxs, func(a, b int) int { return sites[a].column - sites[b].column })
	for _, c := range cols {
		nearest := -1
		for _, i := range idxs {
			if sites[i].column > c {
				break
			}
			nearest = i
		}
		if nearest >= 0 {
			sites[nearest].Ignored = true
		}
	}
}
