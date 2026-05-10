// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/log"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// namedSourceFixtureFunc provides a stable top-level declaration for cache tests.
func namedSourceFixtureFunc() {}

func namedSourceFixtureRuntimeFunc() *runtime.Func {
	return runtime.FuncForPC(reflect.ValueOf(namedSourceFixtureFunc).Pointer())
}

func literalSourceFixtureRuntimeFunc() *runtime.Func {
	literal := func() {}
	return runtime.FuncForPC(reflect.ValueOf(literal).Pointer())
}

// resetSourceCacheTestState installs repository metadata so sourcecache tests also work with -trimpath.
func resetSourceCacheTestState(t *testing.T) {
	t.Helper()

	resetCIVisibilityStateForTesting()
	utils.AddCITagsMap(map[string]string{
		constants.CIWorkspacePath:  sourcePathRepositoryRoot(t),
		constants.GitRepositoryURL: "https://github.com/DataDog/dd-trace-go.git",
	})
	t.Cleanup(resetCIVisibilityStateForTesting)
}

// filesystemPathForRuntimeSource resolves a runtime source path into the file path used by source parsing.
func filesystemPathForRuntimeSource(runtimePath string) string {
	return resolveTestSourcePath(runtimePath).FilesystemPath
}

func TestLoadSourceFileMetadataCachesValidFiles(t *testing.T) {
	resetSourceCacheTestState(t)

	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	filesystemPath := filesystemPathForRuntimeSource(file)

	first := loadSourceFileMetadata(filesystemPath)
	second := loadSourceFileMetadata(filesystemPath)

	require.True(t, first.parseOK)
	assert.Equal(t, first, second)
}

func TestLoadSourceFileMetadataNegativeCachesParseFailures(t *testing.T) {
	resetSourceCacheTestState(t)

	tmpFile, err := os.CreateTemp(t.TempDir(), "sourcecache-invalid-*.go")
	require.NoError(t, err)
	_, err = tmpFile.WriteString("package integrations\nfunc broken( {\n")
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())

	first := loadSourceFileMetadata(tmpFile.Name())
	second := loadSourceFileMetadata(tmpFile.Name())

	assert.False(t, first.parseOK)
	require.Error(t, first.parseErr)
	assert.Equal(t, first, second)
}

func TestLoadSourceFileMetadataHandlesDeclarationsWithoutBody(t *testing.T) {
	resetSourceCacheTestState(t)

	tmpFile, err := os.CreateTemp(t.TempDir(), "sourcecache-decl-*.go")
	require.NoError(t, err)
	_, err = tmpFile.WriteString("package integrations\nfunc external()\nfunc real() {}\n")
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())

	metadata := loadSourceFileMetadata(tmpFile.Name())

	require.True(t, metadata.parseOK)
	require.Len(t, metadata.namedFunctions["external"], 1)
	assert.Equal(t, 0, metadata.namedFunctions["external"][0].bodyStartLine)
	assert.Equal(t, 0, metadata.namedFunctions["external"][0].endLine)
	require.Len(t, metadata.namedFunctions["real"], 1)
	assert.NotZero(t, metadata.namedFunctions["real"][0].bodyStartLine)
	assert.NotZero(t, metadata.namedFunctions["real"][0].endLine)
}

func TestIsFuncNShortName(t *testing.T) {
	for _, name := range []string{"func1", "func12"} {
		assert.True(t, isFuncNShortName(name), name)
	}

	for _, name := range []string{"func", "Func1", "func1x", "TestFunc1", "func1-fm", "func1[...]"} {
		assert.False(t, isFuncNShortName(name), name)
	}
}

func TestFindLineConfirmedDeclaration(t *testing.T) {
	functions := []namedFunctionMetadata{
		{
			declStartLine: 10,
			bodyStartLine: 12,
			endLine:       20,
		},
		{
			declStartLine: 30,
			bodyStartLine: 32,
			endLine:       40,
		},
	}

	for _, runtimeStartLine := range []int{10, 15, 20} {
		function, ok := findLineConfirmedDeclaration(functions, runtimeStartLine)
		require.True(t, ok)
		assert.Equal(t, 10, function.declStartLine)
	}

	function, ok := findLineConfirmedDeclaration(functions, 35)
	require.True(t, ok)
	assert.Equal(t, 30, function.declStartLine)

	_, ok = findLineConfirmedDeclaration([]namedFunctionMetadata{{
		declStartLine: 50,
		endLine:       0,
	}}, 50)
	assert.False(t, ok)

	_, ok = findLineConfirmedDeclaration(functions, 25)
	assert.False(t, ok)
}

func TestFindMatchingFunctionLiteral(t *testing.T) {
	literals := []functionLiteralMetadata{
		{
			bodyStartLine: 10,
			endLine:       12,
		},
		{
			bodyStartLine: 20,
			endLine:       22,
		},
		{
			bodyStartLine: 30,
			endLine:       32,
		},
	}

	literal, inspectedLiterals, ok := findMatchingFunctionLiteral(literals, 19)
	require.True(t, ok)
	assert.Equal(t, 20, literal.bodyStartLine)
	assert.Equal(t, literals, inspectedLiterals)

	literal, inspectedLiterals, ok = findMatchingFunctionLiteral([]functionLiteralMetadata{
		{
			bodyStartLine: 10,
			endLine:       12,
		},
		{
			bodyStartLine: 11,
			endLine:       13,
		},
		{
			bodyStartLine: 30,
			endLine:       32,
		},
	}, 11)
	require.True(t, ok)
	assert.Equal(t, 11, literal.bodyStartLine)
	assert.Equal(t, []functionLiteralMetadata{
		{
			bodyStartLine: 10,
			endLine:       12,
		},
		{
			bodyStartLine: 11,
			endLine:       13,
		},
	}, inspectedLiterals)

	literal, inspectedLiterals, ok = findMatchingFunctionLiteral(literals, 10)
	require.True(t, ok)
	assert.Equal(t, 10, literal.bodyStartLine)
	assert.Equal(t, []functionLiteralMetadata{literals[0]}, inspectedLiterals)

	_, inspectedLiterals, ok = findMatchingFunctionLiteral(literals, 50)
	assert.False(t, ok)
	assert.Equal(t, literals, inspectedLiterals)
}

func TestResolveSourceLocationUsesNamedDeclarationEndLine(t *testing.T) {
	resolution := resolveSourceLocation(sourceFileMetadata{
		namedFunctions: map[string][]namedFunctionMetadata{
			"fixture": {{
				declStartLine:   10,
				bodyStartLine:   12,
				endLine:         20,
				testUnskippable: true,
			}},
		},
	}, "fixture", 14)

	assert.Equal(t, 14, resolution.startLine)
	assert.Equal(t, 20, resolution.endLine)
	assert.True(t, resolution.functionUnskippable)
	require.NotNil(t, resolution.matchedDeclaration)
	assert.Nil(t, resolution.matchedLiteral)
}

func TestResolveSourceLocationDisambiguatesSameNamedDeclarationsByRuntimeStartLine(t *testing.T) {
	resolution := resolveSourceLocation(sourceFileMetadata{
		namedFunctions: map[string][]namedFunctionMetadata{
			"fixture": {
				{
					declStartLine:   10,
					bodyStartLine:   12,
					endLine:         15,
					testUnskippable: false,
				},
				{
					declStartLine:   20,
					bodyStartLine:   22,
					endLine:         28,
					testUnskippable: true,
				},
			},
		},
	}, "fixture", 24)

	assert.Equal(t, 24, resolution.startLine)
	assert.Equal(t, 28, resolution.endLine)
	assert.True(t, resolution.functionUnskippable)
	require.NotNil(t, resolution.matchedDeclaration)
	assert.Equal(t, 20, resolution.matchedDeclaration.declStartLine)
}

func TestResolveSourceLocationUsesLineConfirmedFunc1Declaration(t *testing.T) {
	resolution := resolveSourceLocation(sourceFileMetadata{
		namedFunctions: map[string][]namedFunctionMetadata{
			"func1": {{
				declStartLine:   10,
				bodyStartLine:   12,
				endLine:         20,
				testUnskippable: true,
			}},
		},
		functionLiterals: []functionLiteralMetadata{{
			bodyStartLine: 14,
			endLine:       99,
		}},
	}, "func1", 14)

	assert.Equal(t, 14, resolution.startLine)
	assert.Equal(t, 20, resolution.endLine)
	assert.True(t, resolution.functionUnskippable)
	require.NotNil(t, resolution.matchedDeclaration)
	assert.Nil(t, resolution.matchedLiteral)
	assert.Empty(t, resolution.inspectedLiterals)
}

func TestResolveSourceLocationDisambiguatesFunc1MethodsByRuntimeStartLine(t *testing.T) {
	resolution := resolveSourceLocation(sourceFileMetadata{
		namedFunctions: map[string][]namedFunctionMetadata{
			"func1": {
				{
					declStartLine:   10,
					bodyStartLine:   12,
					endLine:         15,
					testUnskippable: false,
				},
				{
					declStartLine:   20,
					bodyStartLine:   22,
					endLine:         28,
					testUnskippable: true,
				},
			},
		},
	}, "func1", 24)

	assert.Equal(t, 24, resolution.startLine)
	assert.Equal(t, 28, resolution.endLine)
	assert.True(t, resolution.functionUnskippable)
	require.NotNil(t, resolution.matchedDeclaration)
	assert.Equal(t, 20, resolution.matchedDeclaration.declStartLine)
}

func TestResolveSourceLocationMatchesFunc1LiteralWhenDeclarationIsUnrelated(t *testing.T) {
	resolution := resolveSourceLocation(sourceFileMetadata{
		namedFunctions: map[string][]namedFunctionMetadata{
			"func1": {{
				declStartLine:   10,
				bodyStartLine:   12,
				endLine:         20,
				testUnskippable: true,
			}},
		},
		functionLiterals: []functionLiteralMetadata{{
			bodyStartLine: 50,
			endLine:       60,
		}},
	}, "func1", 49)

	assert.Equal(t, 50, resolution.startLine)
	assert.Equal(t, 60, resolution.endLine)
	assert.False(t, resolution.functionUnskippable)
	assert.Nil(t, resolution.matchedDeclaration)
	require.NotNil(t, resolution.matchedLiteral)
	assert.Len(t, resolution.inspectedLiterals, 1)
}

// TestResolveSourceLocationPrefersExactFuncNLiteralOverEarlierTolerated verifies generated closures use exact line matches first.
func TestResolveSourceLocationPrefersExactFuncNLiteralOverEarlierTolerated(t *testing.T) {
	resolution := resolveSourceLocation(sourceFileMetadata{
		functionLiterals: []functionLiteralMetadata{
			{
				bodyStartLine: 10,
				endLine:       12,
			},
			{
				bodyStartLine: 11,
				endLine:       13,
			},
		},
	}, "func2", 11)

	assert.Equal(t, 11, resolution.startLine)
	assert.Equal(t, 13, resolution.endLine)
	assert.Nil(t, resolution.matchedDeclaration)
	require.NotNil(t, resolution.matchedLiteral)
	assert.Equal(t, 11, resolution.matchedLiteral.bodyStartLine)
	assert.Equal(t, []functionLiteralMetadata{
		{
			bodyStartLine: 10,
			endLine:       12,
		},
		{
			bodyStartLine: 11,
			endLine:       13,
		},
	}, resolution.inspectedLiterals)
}

func TestResolveSourceLocationMatchesFunc1LiteralWhenDeclarationHasNoBody(t *testing.T) {
	resolution := resolveSourceLocation(sourceFileMetadata{
		namedFunctions: map[string][]namedFunctionMetadata{
			"func1": {{
				declStartLine:   10,
				testUnskippable: true,
			}},
		},
		functionLiterals: []functionLiteralMetadata{{
			bodyStartLine: 30,
			endLine:       35,
		}},
	}, "func1", 31)

	assert.Equal(t, 30, resolution.startLine)
	assert.Equal(t, 35, resolution.endLine)
	assert.False(t, resolution.functionUnskippable)
	assert.Nil(t, resolution.matchedDeclaration)
	require.NotNil(t, resolution.matchedLiteral)
	assert.Len(t, resolution.inspectedLiterals, 1)
}

func TestResolveSourceLocationLeavesUnmatchedFunc1Unresolved(t *testing.T) {
	resolution := resolveSourceLocation(sourceFileMetadata{
		namedFunctions: map[string][]namedFunctionMetadata{
			"func1": {{
				declStartLine:   10,
				bodyStartLine:   12,
				endLine:         20,
				testUnskippable: true,
			}},
		},
		functionLiterals: []functionLiteralMetadata{{
			bodyStartLine: 80,
			endLine:       90,
		}},
	}, "func1", 50)

	assert.Equal(t, 50, resolution.startLine)
	assert.Zero(t, resolution.endLine)
	assert.False(t, resolution.functionUnskippable)
	assert.Nil(t, resolution.matchedDeclaration)
	assert.Nil(t, resolution.matchedLiteral)
	assert.Len(t, resolution.inspectedLiterals, 1)
}

func TestResolveSourceLocationKeepsNonFuncNDeclarationFallback(t *testing.T) {
	resolution := resolveSourceLocation(sourceFileMetadata{
		namedFunctions: map[string][]namedFunctionMetadata{
			"fixture": {{
				declStartLine:   10,
				bodyStartLine:   12,
				endLine:         20,
				testUnskippable: true,
			}},
		},
		functionLiterals: []functionLiteralMetadata{{
			bodyStartLine: 50,
			endLine:       60,
		}},
	}, "fixture", 49)

	assert.Equal(t, 49, resolution.startLine)
	assert.Equal(t, 20, resolution.endLine)
	assert.True(t, resolution.functionUnskippable)
	require.NotNil(t, resolution.matchedDeclaration)
	assert.Nil(t, resolution.matchedLiteral)
	assert.Empty(t, resolution.inspectedLiterals)
}

func TestResolveSourceLocationAdjustsLiteralStartLine(t *testing.T) {
	resolution := resolveSourceLocation(sourceFileMetadata{
		functionLiterals: []functionLiteralMetadata{{
			bodyStartLine: 23,
			endLine:       29,
		}},
	}, "fixture", 22)

	assert.Equal(t, 23, resolution.startLine)
	assert.Equal(t, 29, resolution.endLine)
	assert.False(t, resolution.functionUnskippable)
	assert.Nil(t, resolution.matchedDeclaration)
	require.NotNil(t, resolution.matchedLiteral)
	assert.Len(t, resolution.inspectedLiterals, 1)
}

func TestResolveSourceLocationLeavesNoMatchUnchanged(t *testing.T) {
	resolution := resolveSourceLocation(sourceFileMetadata{
		functionLiterals: []functionLiteralMetadata{{
			bodyStartLine: 50,
			endLine:       60,
		}},
	}, "missing", 10)

	assert.Equal(t, 10, resolution.startLine)
	assert.Zero(t, resolution.endLine)
	assert.Nil(t, resolution.matchedDeclaration)
	assert.Nil(t, resolution.matchedLiteral)
	assert.Len(t, resolution.inspectedLiterals, 1)
}

func TestSetTestFuncKeepsRealFunc1DeclarationWhenLineConfirmed(t *testing.T) {
	resetSourceCacheTestState(t)
	mockTracer.Reset()

	recordLogger := new(log.RecordLogger)
	oldLevel := log.GetLevel()
	defer log.UseLogger(recordLogger)()
	log.SetLevel(log.LevelDebug)
	defer log.SetLevel(oldLevel)

	now := time.Now()
	session, module, suite, test := createDDTest(now)
	defer func() {
		session.Close(0)
		module.Close()
		suite.Close()
	}()

	fn := runtime.FuncForPC(reflect.ValueOf(func1).Pointer())
	require.NotNil(t, fn)
	file, runtimeStartLine := fn.FileLine(fn.Entry())
	require.Equal(t, "manual_api_sourcecache_funcn_fixture_test.go", filepath.Base(file))

	metadata := loadSourceFileMetadata(filesystemPathForRuntimeSource(file))
	require.True(t, metadata.parseOK)
	require.Len(t, metadata.namedFunctions["func1"], 1)
	declaration := metadata.namedFunctions["func1"][0]

	test.SetTestFunc(fn)

	startLine, startOK := test.GetTag(constants.TestSourceStartLine)
	endLine, endOK := test.GetTag(constants.TestSourceEndLine)
	require.True(t, startOK)
	require.True(t, endOK)
	assert.Equal(t, float64(runtimeStartLine), startLine)
	assert.Equal(t, float64(declaration.endLine), endLine)

	unskippable, ok := test.GetTag(constants.TestUnskippable)
	require.True(t, ok)
	assert.Equal(t, "true", unskippable)
	assert.Equal(t, true, test.Context().Value(constants.TestUnskippable))

	logs := recordLogger.Logs()
	assert.Equal(t, 1, countSourceResolutionLogLinesForFunction(logs, fn.Name(), "matched AST function declaration"))
	assert.Equal(t, 0, countSourceResolutionLogLinesForFunction(logs, fn.Name(), "matched AST function literal"))
	assert.Equal(t, 1, countSourceResolutionLogLinesForFunction(logs, fn.Name(), "resolved test source range"))
}

func TestSetTestFuncResolvesGeneratedFunc1LiteralWhenDeclarationExists(t *testing.T) {
	resetSourceCacheTestState(t)
	mockTracer.Reset()

	recordLogger := new(log.RecordLogger)
	oldLevel := log.GetLevel()
	defer log.UseLogger(recordLogger)()
	log.SetLevel(log.LevelDebug)
	defer log.SetLevel(oldLevel)

	now := time.Now()
	session, module, suite, test := createDDTest(now)
	defer func() {
		session.Close(0)
		module.Close()
		suite.Close()
	}()

	fn := func1ShadowClosureRuntimeFunc()
	require.NotNil(t, fn)
	require.True(t, strings.HasSuffix(fn.Name(), ".func1"), fn.Name())
	file, runtimeStartLine := fn.FileLine(fn.Entry())
	require.Equal(t, "manual_api_sourcecache_funcn_fixture_test.go", filepath.Base(file))

	metadata := loadSourceFileMetadata(filesystemPathForRuntimeSource(file))
	require.True(t, metadata.parseOK)
	require.Len(t, metadata.namedFunctions["func1"], 1)
	require.Len(t, metadata.functionLiterals, 1)

	declaration := metadata.namedFunctions["func1"][0]
	literal := metadata.functionLiterals[0]
	delta := literal.bodyStartLine - runtimeStartLine
	require.GreaterOrEqual(t, delta, -1)
	require.LessOrEqual(t, delta, 1)

	test.SetTestFunc(fn)

	startLine, startOK := test.GetTag(constants.TestSourceStartLine)
	endLine, endOK := test.GetTag(constants.TestSourceEndLine)
	require.True(t, startOK)
	require.True(t, endOK)
	assert.Equal(t, float64(literal.bodyStartLine), startLine)
	assert.Equal(t, float64(literal.endLine), endLine)
	assert.NotEqual(t, float64(declaration.endLine), endLine)

	_, unskippableTagged := test.GetTag(constants.TestUnskippable)
	assert.False(t, unskippableTagged)
	assert.Nil(t, test.Context().Value(constants.TestUnskippable))

	logs := recordLogger.Logs()
	assert.Equal(t, 1, countSourceResolutionLogLinesForFunction(logs, fn.Name(), "inspecting AST function literal candidate"))
	assert.Equal(t, 1, countSourceResolutionLogLinesForFunction(logs, fn.Name(), "matched AST function literal"))
	assert.Equal(t, 0, countSourceResolutionLogLinesForFunction(logs, fn.Name(), "matched AST function declaration"))
	assert.Equal(t, 0, countSourceResolutionLogLinesForFunction(logs, fn.Name(), "test source range incomplete"))
	assert.Equal(t, 1, countSourceResolutionLogLinesForFunction(logs, fn.Name(), "resolved test source range"))
}

// TestSetTestFuncResolvesAdjacentFuncNLiteralToExactSourceRange verifies adjacent closures resolve to their own source ranges.
func TestSetTestFuncResolvesAdjacentFuncNLiteralToExactSourceRange(t *testing.T) {
	resetSourceCacheTestState(t)
	mockTracer.Reset()

	recordLogger := new(log.RecordLogger)
	oldLevel := log.GetLevel()
	defer log.UseLogger(recordLogger)()
	log.SetLevel(log.LevelDebug)
	defer log.SetLevel(oldLevel)

	now := time.Now()
	session, module, suite, test := createDDTest(now)
	defer func() {
		session.Close(0)
		module.Close()
		suite.Close()
	}()

	firstFn, secondFn := adjacentLiteralRuntimeFuncs()
	require.NotNil(t, firstFn)
	require.NotNil(t, secondFn)
	require.True(t, isFuncNShortName(secondFn.Name()[strings.LastIndex(secondFn.Name(), ".")+1:]), secondFn.Name())

	file, secondRuntimeStartLine := secondFn.FileLine(secondFn.Entry())
	require.Equal(t, "manual_api_sourcecache_adjacent_literal_fixture_test.go", filepath.Base(file))

	metadata := loadSourceFileMetadata(filesystemPathForRuntimeSource(file))
	require.True(t, metadata.parseOK)
	require.Len(t, metadata.functionLiterals, 2)
	firstLiteral := metadata.functionLiterals[0]
	secondLiteral := metadata.functionLiterals[1]
	require.Equal(t, firstLiteral.bodyStartLine+1, secondLiteral.bodyStartLine)
	require.Equal(t, secondLiteral.bodyStartLine, secondRuntimeStartLine)

	test.SetTestFunc(secondFn)

	startLine, startOK := test.GetTag(constants.TestSourceStartLine)
	endLine, endOK := test.GetTag(constants.TestSourceEndLine)
	require.True(t, startOK)
	require.True(t, endOK)
	assert.Equal(t, float64(secondLiteral.bodyStartLine), startLine)
	assert.Equal(t, float64(secondLiteral.endLine), endLine)
	assert.NotEqual(t, float64(firstLiteral.bodyStartLine), startLine)
	assert.NotEqual(t, float64(firstLiteral.endLine), endLine)

	logs := recordLogger.Logs()
	assert.Equal(t, 1, countSourceResolutionLogLinesForFunction(logs, secondFn.Name(), fmt.Sprintf("literal_start_line:%d", firstLiteral.bodyStartLine)))
	assert.Equal(t, 1, countSourceResolutionLogLinesForFunction(logs, secondFn.Name(), fmt.Sprintf("literal_start_line:%d", secondLiteral.bodyStartLine)))
	assert.Equal(t, 1, countSourceResolutionLogLinesForFunction(logs, secondFn.Name(), "matched AST function literal"))
	assert.Equal(t, 0, countSourceResolutionLogLinesForFunction(logs, secondFn.Name(), "matched AST function declaration"))
	assert.Equal(t, 1, countSourceResolutionLogLinesForFunction(logs, secondFn.Name(), "resolved test source range"))
}

func TestLoadSourceFileMetadataIsConcurrentSafe(t *testing.T) {
	resetSourceCacheTestState(t)

	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	filesystemPath := filesystemPathForRuntimeSource(file)

	results := make([]sourceFileMetadata, 16)
	var wg sync.WaitGroup
	for idx := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = loadSourceFileMetadata(filesystemPath)
		}(idx)
	}
	wg.Wait()

	for idx := 1; idx < len(results); idx++ {
		assert.Equal(t, results[0], results[idx])
	}
}

func TestSetTestFuncCachesNamedFunctionSourceTagsAndLogs(t *testing.T) {
	resetSourceCacheTestState(t)
	mockTracer.Reset()

	recordLogger := new(log.RecordLogger)
	oldLevel := log.GetLevel()
	defer log.UseLogger(recordLogger)()
	log.SetLevel(log.LevelDebug)
	defer log.SetLevel(oldLevel)

	now := time.Now()
	session, module, suite, test := createDDTest(now)
	defer func() {
		session.Close(0)
		module.Close()
		suite.Close()
	}()

	fn := namedSourceFixtureRuntimeFunc()
	test.SetTestFunc(fn)
	firstStartLine, firstStartOK := test.GetTag(constants.TestSourceStartLine)
	firstEndLine, firstEndOK := test.GetTag(constants.TestSourceEndLine)
	require.True(t, firstStartOK)
	require.True(t, firstEndOK)

	test.SetTestFunc(fn)
	secondStartLine, secondStartOK := test.GetTag(constants.TestSourceStartLine)
	secondEndLine, secondEndOK := test.GetTag(constants.TestSourceEndLine)
	require.True(t, secondStartOK)
	require.True(t, secondEndOK)

	assert.Equal(t, firstStartLine, secondStartLine)
	assert.Equal(t, firstEndLine, secondEndLine)

	logs := recordLogger.Logs()
	assert.Equal(t, 2, countSourceResolutionLogLines(logs, "resolving test source location"))
	assert.Equal(t, 2, countSourceResolutionLogLines(logs, "matched AST function declaration"))
	assert.Equal(t, 2, countSourceResolutionLogLines(logs, "resolved test source range"))
}

func TestSetTestFuncCachesFunctionLiteralSourceTagsAndLogs(t *testing.T) {
	resetSourceCacheTestState(t)
	mockTracer.Reset()

	recordLogger := new(log.RecordLogger)
	oldLevel := log.GetLevel()
	defer log.UseLogger(recordLogger)()
	log.SetLevel(log.LevelDebug)
	defer log.SetLevel(oldLevel)

	now := time.Now()
	session, module, suite, test := createDDTest(now)
	defer func() {
		session.Close(0)
		module.Close()
		suite.Close()
	}()

	fn := literalSourceFixtureRuntimeFunc()
	test.SetTestFunc(fn)
	firstStartLine, firstStartOK := test.GetTag(constants.TestSourceStartLine)
	firstEndLine, firstEndOK := test.GetTag(constants.TestSourceEndLine)
	require.True(t, firstStartOK)
	require.True(t, firstEndOK)

	test.SetTestFunc(fn)
	secondStartLine, secondStartOK := test.GetTag(constants.TestSourceStartLine)
	secondEndLine, secondEndOK := test.GetTag(constants.TestSourceEndLine)
	require.True(t, secondStartOK)
	require.True(t, secondEndOK)

	assert.Equal(t, firstStartLine, secondStartLine)
	assert.Equal(t, firstEndLine, secondEndLine)

	logs := recordLogger.Logs()
	assert.Equal(t, 2, countSourceResolutionLogLines(logs, "resolving test source location"))
	assert.Equal(t, 2, countSourceResolutionLogLines(logs, "inspecting AST function literal candidate"))
	assert.Equal(t, 2, countSourceResolutionLogLines(logs, "matched AST function literal"))
	assert.Equal(t, 2, countSourceResolutionLogLines(logs, "resolved test source range"))
}

func TestSetTestFuncPreservesSuiteLevelUnskippable(t *testing.T) {
	resetSourceCacheTestState(t)
	mockTracer.Reset()

	now := time.Now()
	session, module, suite, test := createDDTest(now)
	defer func() {
		session.Close(0)
		module.Close()
		suite.Close()
	}()

	test.SetTestFunc(runtime.FuncForPC(reflect.ValueOf(suiteUnskippableFixtureFunc).Pointer()))

	unskippable, ok := test.GetTag(constants.TestUnskippable)
	require.True(t, ok)
	assert.Equal(t, "true", unskippable)
	assert.Equal(t, true, test.Context().Value(constants.TestUnskippable))
}

func TestSetTestFuncPreservesDeclarationLevelUnskippable(t *testing.T) {
	resetSourceCacheTestState(t)
	mockTracer.Reset()

	now := time.Now()
	session, module, suite, test := createDDTest(now)
	defer func() {
		session.Close(0)
		module.Close()
		suite.Close()
	}()

	test.SetTestFunc(runtime.FuncForPC(reflect.ValueOf(declarationUnskippableFixtureFunc).Pointer()))

	unskippable, ok := test.GetTag(constants.TestUnskippable)
	require.True(t, ok)
	assert.Equal(t, "true", unskippable)
	assert.Equal(t, true, test.Context().Value(constants.TestUnskippable))
}

func TestSetTestFuncDisambiguatesSameNamedMethodsInTheSameFile(t *testing.T) {
	resetSourceCacheTestState(t)
	mockTracer.Reset()

	now := time.Now()
	session, module, suite, firstTest := createDDTest(now)
	defer func() {
		session.Close(0)
		module.Close()
		suite.Close()
	}()

	secondTest := suite.CreateTest("my-second-test", WithTestStartTime(now))

	firstFn := runtime.FuncForPC(reflect.ValueOf(sameNameFixtureSuiteA.TestSharedName).Pointer())
	secondFn := runtime.FuncForPC(reflect.ValueOf(sameNameFixtureSuiteB.TestSharedName).Pointer())
	require.NotNil(t, firstFn)
	require.NotNil(t, secondFn)
	firstFile, firstRuntimeStartLine := firstFn.FileLine(firstFn.Entry())
	secondFile, secondRuntimeStartLine := secondFn.FileLine(secondFn.Entry())
	require.Equal(t, firstFile, secondFile)
	metadata := loadSourceFileMetadata(filesystemPathForRuntimeSource(firstFile))
	require.Len(t, metadata.namedFunctions["TestSharedName"], 2)

	firstTest.SetTestFunc(firstFn)
	secondTest.SetTestFunc(secondFn)

	assert.Nil(t, firstTest.Context().Value(constants.TestUnskippable))

	secondUnskippable, secondTagged := secondTest.GetTag(constants.TestUnskippable)
	require.True(t, secondTagged)
	assert.Equal(t, "true", secondUnskippable)
	assert.Equal(t, true, secondTest.Context().Value(constants.TestUnskippable))

	firstStartLine, firstStartOK := firstTest.GetTag(constants.TestSourceStartLine)
	firstEndLine, firstEndOK := firstTest.GetTag(constants.TestSourceEndLine)
	secondStartLine, secondStartOK := secondTest.GetTag(constants.TestSourceStartLine)
	secondEndLine, secondEndOK := secondTest.GetTag(constants.TestSourceEndLine)
	require.True(t, firstStartOK)
	require.True(t, firstEndOK)
	require.True(t, secondStartOK)
	require.True(t, secondEndOK)
	assert.Equal(t, float64(firstRuntimeStartLine), firstStartLine)
	assert.Equal(t, float64(secondRuntimeStartLine), secondStartLine)
	assert.Equal(t, float64(metadata.namedFunctions["TestSharedName"][0].endLine), firstEndLine)
	assert.Equal(t, float64(metadata.namedFunctions["TestSharedName"][1].endLine), secondEndLine)
	assert.NotEqual(t, firstEndLine, secondEndLine)
}

func countSourceResolutionLogLines(lines []string, want string) int {
	count := 0
	for _, line := range lines {
		if strings.Contains(line, want) {
			count++
		}
	}
	return count
}

// countSourceResolutionLogLinesForFunction counts source-resolution logs for one runtime function name.
func countSourceResolutionLogLinesForFunction(lines []string, functionName, want string) int {
	count := 0
	functionToken := "function:" + functionName + " "
	for _, line := range lines {
		if strings.Contains(line, want) && strings.Contains(line, functionToken) {
			count++
		}
	}
	return count
}
