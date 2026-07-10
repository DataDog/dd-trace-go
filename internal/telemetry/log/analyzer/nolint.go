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
// mirroring golangci-lint's own nolint directive syntax.
var nolintPattern = regexp.MustCompile(`^//\s*nolint(?::\s*([\w, -]+))?`)

// nolintSuppressed reports whether pos is covered by a //nolint comment that
// applies to one of names (case-insensitive), or a bare //nolint with no
// linter list. Matching golangci-lint's own nolint processor, a directive
// counts whether it trails on the same line as pos or stands alone on the
// line immediately above it (the common "doc comment before a statement"
// style).
//
// telemetrysafety and logformatverbs replace checks that used to run under
// golangci-lint's gocritic/ruleguard linter, which understands //nolint
// directives. Running as a standalone go vet pass (make lint/errlog) has no
// such mechanism built in, so pre-existing `//nolint:gocritic` exceptions on
// call sites this migration inherited would otherwise start failing even
// though they were deliberately excepted. This keeps those exceptions honored
// without requiring a churn of call-site edits that are out of scope here.
func nolintSuppressed(pass *analysis.Pass, pos token.Pos, names ...string) bool {
	file := fileForPos(pass, pos)
	if file == nil {
		return false
	}
	line := pass.Fset.Position(pos).Line
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			cLine := pass.Fset.Position(c.Pos()).Line
			if cLine != line && cLine != line-1 {
				continue
			}
			if nolintMatches(c.Text, names) {
				return true
			}
		}
	}
	return false
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
	for _, n := range strings.Split(m[1], ",") {
		n = strings.TrimSpace(n)
		for _, want := range names {
			if strings.EqualFold(n, want) {
				return true
			}
		}
	}
	return false
}
