// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package analyzer

import (
	"go/ast"
	"go/token"
	"regexp"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// nolintPattern matches a //nolint or //nolint:linter1,linter2 comment,
// mirroring golangci-lint's own nolint directive syntax. The \b word
// boundary after "nolint" keeps ordinary comments whose first word merely
// starts with the prefix — //nolintended behavior, // nolintentionally left
// unsafe — from parsing as a bare, suppress-everything directive.
var nolintPattern = regexp.MustCompile(`^//\s*nolint\b(?::\s*([\w, -]+))?`)

// nolintSuppressed reports whether pos is covered by a //nolint comment that
// applies to one of names (case-insensitive), or a bare //nolint with no
// linter list. Matching golangci-lint's own nolint processor, a directive
// counts whether it trails on the same line as pos or stands alone on a line
// above it (the common "doc comment before a statement" style).
//
// enclosingStart bounds how far up that search goes: for a diagnostic
// reported inside a multi-line call expression, a standalone directive on
// any line between the call's first line and pos — including the line
// directly above the call — applies to pos. This mirrors golangci-lint,
// which anchors a standalone directive to the node that follows it and
// covers that node's whole extent; without it, a directive above
//
//	//nolint:telemetrysafety
//	log.Error("failed",
//		slog.Any("err", err))
//
// would only suppress a diagnostic reported on the call's first line, not on
// the attr line where telemetrysafety reports.
//
// telemetrysafety and logformatverbs replace checks that used to run under
// golangci-lint's gocritic/ruleguard linter, which understands //nolint
// directives. Running as a standalone go vet pass (make lint/errlog) has no
// such mechanism built in, so pre-existing `//nolint:gocritic` exceptions on
// call sites this migration inherited would otherwise start failing even
// though they were deliberately excepted. This keeps those exceptions honored
// without requiring a churn of call-site edits that are out of scope here.
func nolintSuppressed(pass *analysis.Pass, pos, enclosingStart token.Pos, names ...string) bool {
	file := fileForPos(pass, pos)
	if file == nil {
		return false
	}
	line := pass.Fset.Position(pos).Line
	minLine := line
	if enclosingStart.IsValid() {
		if startLine := pass.Fset.Position(enclosingStart).Line; startLine < minLine {
			minLine = startLine
		}
	}
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			cLine := pass.Fset.Position(c.Pos()).Line
			switch {
			case cLine == line:
				// Trailing on pos's own line: always counts, same as golangci-lint.
			case cLine < line && cLine >= minLine-1 && standaloneComment(pass, c):
				// Standalone above pos: directly above it, or anywhere between
				// the enclosing call's first line and pos. A directive trailing
				// unrelated code on one of those lines still does not count.
			default:
				continue
			}
			if nolintMatches(c.Text, names) {
				return true
			}
		}
	}
	return false
}

// standaloneComment reports whether c is the only thing on its source line
// (aside from leading whitespace). If the source can't be read, it degrades
// to true (the pre-existing, looser behavior) rather than failing closed.
func standaloneComment(pass *analysis.Pass, c *ast.Comment) bool {
	if pass.ReadFile == nil {
		return true
	}
	tokFile := pass.Fset.File(c.Pos())
	if tokFile == nil {
		return true
	}
	data, err := pass.ReadFile(tokFile.Name())
	if err != nil {
		return true
	}
	line := pass.Fset.Position(c.Pos()).Line
	lineStartOffset := tokFile.Offset(tokFile.LineStart(line))
	commentOffset := tokFile.Offset(c.Pos())
	if lineStartOffset < 0 || commentOffset > len(data) || lineStartOffset > commentOffset {
		return true
	}
	return len(strings.TrimSpace(string(data[lineStartOffset:commentOffset]))) == 0
}

func fileForPos(pass *analysis.Pass, pos token.Pos) *ast.File {
	tokFile := pass.Fset.File(pos)
	if tokFile == nil {
		return nil
	}
	for _, f := range pass.Files {
		if pass.Fset.File(f.Pos()) == tokFile {
			return f
		}
	}
	return nil
}

func nolintMatches(commentText string, names []string) bool {
	m := nolintPattern.FindStringSubmatch(commentText)
	if m == nil {
		return false
	}
	if strings.TrimSpace(m[1]) == "" {
		return true // bare //nolint suppresses everything
	}
	for n := range strings.SplitSeq(m[1], ",") {
		n = strings.TrimSpace(n)
		for _, want := range names {
			if strings.EqualFold(n, want) {
				return true
			}
		}
	}
	return false
}
