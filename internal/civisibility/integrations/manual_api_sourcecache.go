// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"sync"
)

// sourceFileMetadataCache stores parsed source metadata keyed by absolute file path.
var sourceFileMetadataCache sync.Map

// sourceFileCacheSlot coordinates one-time metadata loading for a single source file.
type sourceFileCacheSlot struct {
	once  sync.Once
	entry sourceFileMetadata
}

// sourceFileMetadata contains the parsed source information needed by SetTestFunc.
type sourceFileMetadata struct {
	parseOK          bool
	parseErr         error
	suiteUnskippable bool
	namedFunctions   map[string][]namedFunctionMetadata
	functionLiterals []functionLiteralMetadata
}

// namedFunctionMetadata stores the source range and tags for a named function declaration.
type namedFunctionMetadata struct {
	declStartLine   int
	bodyStartLine   int
	endLine         int
	testUnskippable bool
}

// functionLiteralMetadata stores the source range for a function literal.
type functionLiteralMetadata struct {
	bodyStartLine int
	endLine       int
}

// sourceResolution describes the cached source location selected for a runtime function.
type sourceResolution struct {
	startLine           int
	endLine             int
	functionUnskippable bool
	matchedDeclaration  *namedFunctionMetadata
	matchedLiteral      *functionLiteralMetadata
	inspectedLiterals   []functionLiteralMetadata
}

// loadSourceFileMetadata parses and caches the metadata needed to resolve test source locations.
func loadSourceFileMetadata(absolutePath string) sourceFileMetadata {
	// Keep one cache slot per file so concurrent first lookups share the same parsing work.
	slotAny, _ := sourceFileMetadataCache.LoadOrStore(absolutePath, &sourceFileCacheSlot{})
	slot := slotAny.(*sourceFileCacheSlot)
	slot.once.Do(func() {
		slot.entry = parseSourceFileMetadata(absolutePath)
	})
	return slot.entry
}

// parseSourceFileMetadata extracts the source metadata required by SetTestFunc for a file.
func parseSourceFileMetadata(absolutePath string) sourceFileMetadata {
	metadata := sourceFileMetadata{
		parseOK:        false,
		namedFunctions: make(map[string][]namedFunctionMetadata),
	}

	// Parse comments as well because source-level ITR hints are encoded in comments.
	fset := token.NewFileSet()
	fileNode, err := parser.ParseFile(fset, absolutePath, nil, parser.AllErrors|parser.ParseComments)
	if err != nil {
		// Keep the parse error in the cache so later calls can preserve the existing debug log
		// without reparsing the same missing or invalid file on every SetTestFunc invocation.
		metadata.parseErr = err
		return metadata
	}

	metadata.parseOK = true
	// Suite-level unskippable is a file property, so compute it once and reuse it for every test in the file.
	metadata.suiteUnskippable = commentGroupsContain(fileNode.Comments, "//dd:suite.unskippable")

	ast.Inspect(fileNode, func(n ast.Node) bool {
		if funcDecl, ok := n.(*ast.FuncDecl); ok {
			var docComments []*ast.Comment
			if funcDecl.Doc != nil {
				docComments = funcDecl.Doc.List
			}

			function := namedFunctionMetadata{
				declStartLine:   fset.Position(funcDecl.Pos()).Line,
				testUnskippable: commentListContains(docComments, "//dd:test.unskippable"),
			}
			if funcDecl.Body != nil {
				// Keep the same body-based range the old SetTestFunc implementation used for end-line resolution.
				function.bodyStartLine = fset.Position(funcDecl.Body.Pos()).Line
				function.endLine = fset.Position(funcDecl.Body.End()).Line
			}
			// Preserve the current matching behavior by keeping declarations in AST traversal order.
			metadata.namedFunctions[funcDecl.Name.Name] = append(metadata.namedFunctions[funcDecl.Name.Name], function)
			return true
		}

		if funcLit, ok := n.(*ast.FuncLit); ok {
			// Literals do not have stable names, so SetTestFunc still matches them by approximate body start line.
			metadata.functionLiterals = append(metadata.functionLiterals, functionLiteralMetadata{
				bodyStartLine: fset.Position(funcLit.Body.Pos()).Line,
				endLine:       fset.Position(funcLit.Body.End()).Line,
			})
		}

		return true
	})

	return metadata
}

// resolveSourceLocation matches a runtime function against cached source metadata.
func resolveSourceLocation(metadata sourceFileMetadata, shortName string, runtimeStartLine int) sourceResolution {
	resolution := sourceResolution{startLine: runtimeStartLine}
	functions := metadata.namedFunctions[shortName]

	if matchedDeclaration, ok := findLineConfirmedDeclaration(functions, runtimeStartLine); ok {
		// Named declarations keep the runtime-derived start line for compatibility with the old implementation.
		resolution.endLine = matchedDeclaration.endLine
		resolution.functionUnskippable = matchedDeclaration.testUnskippable
		resolution.matchedDeclaration = &matchedDeclaration
		return resolution
	}

	if isFuncNShortName(shortName) {
		matchedLiteral, inspectedLiterals, ok := findMatchingFunctionLiteral(metadata.functionLiterals, runtimeStartLine)
		resolution.inspectedLiterals = inspectedLiterals
		if !ok {
			return resolution
		}

		resolution.startLine = matchedLiteral.bodyStartLine
		resolution.endLine = matchedLiteral.endLine
		resolution.matchedLiteral = &matchedLiteral
		return resolution
	}

	if len(functions) > 0 {
		// Preserve the original fallback for non-generated names: if no declaration contains the
		// runtime line, use the first declaration in source order rather than switching to literals.
		matchedDeclaration := functions[0]
		resolution.endLine = matchedDeclaration.endLine
		resolution.functionUnskippable = matchedDeclaration.testUnskippable
		resolution.matchedDeclaration = &matchedDeclaration
		return resolution
	}

	// Preserve the old "first literal within one line" heuristic because runtime line numbers for
	// literals point at the first instruction rather than the literal declaration itself.
	matchedLiteral, inspectedLiterals, ok := findMatchingFunctionLiteral(metadata.functionLiterals, runtimeStartLine)
	resolution.inspectedLiterals = inspectedLiterals
	if ok {
		resolution.startLine = matchedLiteral.bodyStartLine
		resolution.endLine = matchedLiteral.endLine
		resolution.matchedLiteral = &matchedLiteral
		return resolution
	}

	return resolution
}

// isFuncNShortName reports whether a runtime short name has Go's generated closure shape funcN.
func isFuncNShortName(shortName string) bool {
	if len(shortName) <= len("func") || !strings.HasPrefix(shortName, "func") {
		return false
	}
	for idx := len("func"); idx < len(shortName); idx++ {
		if shortName[idx] < '0' || shortName[idx] > '9' {
			return false
		}
	}
	return true
}

// findLineConfirmedDeclaration returns the first declaration whose source range contains the runtime line.
func findLineConfirmedDeclaration(functions []namedFunctionMetadata, runtimeStartLine int) (namedFunctionMetadata, bool) {
	for _, function := range functions {
		if function.declStartLine > 0 &&
			function.endLine >= function.declStartLine &&
			function.declStartLine <= runtimeStartLine &&
			runtimeStartLine <= function.endLine {
			return function, true
		}
	}
	return namedFunctionMetadata{}, false
}

// findMatchingFunctionLiteral returns the exact line match when present, otherwise the closest tolerated literal.
func findMatchingFunctionLiteral(literals []functionLiteralMetadata, runtimeStartLine int) (functionLiteralMetadata, []functionLiteralMetadata, bool) {
	inspectedLiterals := make([]functionLiteralMetadata, 0, len(literals))
	var fallback functionLiteralMetadata
	var fallbackDelta int
	fallbackFound := false
	for idx := range literals {
		literal := literals[idx]
		inspectedLiterals = append(inspectedLiterals, literal)

		delta := literal.bodyStartLine - runtimeStartLine
		if delta == 0 {
			return literal, inspectedLiterals, true
		}
		if delta < -1 || delta > 1 {
			continue
		}

		absDelta := delta
		if absDelta < 0 {
			absDelta = -absDelta
		}
		if !fallbackFound || absDelta < fallbackDelta {
			fallback = literal
			fallbackDelta = absDelta
			fallbackFound = true
		}
	}

	if fallbackFound {
		return fallback, inspectedLiterals, true
	}

	return functionLiteralMetadata{}, inspectedLiterals, false
}

func commentGroupsContain(commentGroups []*ast.CommentGroup, needle string) bool {
	for _, commentGroup := range commentGroups {
		if commentListContains(commentGroup.List, needle) {
			return true
		}
	}
	return false
}

func commentListContains(comments []*ast.Comment, needle string) bool {
	for _, comment := range comments {
		if strings.Contains(comment.Text, needle) {
			return true
		}
	}
	return false
}
