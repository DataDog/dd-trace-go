// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
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

func TestLoadSourceFileMetadataCachesValidFiles(t *testing.T) {
	resetCIVisibilityStateForTesting()

	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)

	first := loadSourceFileMetadata(file)
	second := loadSourceFileMetadata(file)

	require.True(t, first.parseOK)
	assert.Equal(t, first, second)
}

func TestLoadSourceFileMetadataNegativeCachesParseFailures(t *testing.T) {
	resetCIVisibilityStateForTesting()

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
	resetCIVisibilityStateForTesting()

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

func TestLoadSourceFileMetadataIsConcurrentSafe(t *testing.T) {
	resetCIVisibilityStateForTesting()

	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)

	results := make([]sourceFileMetadata, 16)
	var wg sync.WaitGroup
	for idx := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = loadSourceFileMetadata(file)
		}(idx)
	}
	wg.Wait()

	for idx := 1; idx < len(results); idx++ {
		assert.Equal(t, results[0], results[idx])
	}
}

func TestSetTestFuncCachesNamedFunctionSourceTagsAndLogs(t *testing.T) {
	resetCIVisibilityStateForTesting()
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
	resetCIVisibilityStateForTesting()
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
	resetCIVisibilityStateForTesting()
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
	resetCIVisibilityStateForTesting()
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

func countSourceResolutionLogLines(lines []string, want string) int {
	count := 0
	for _, line := range lines {
		if strings.Contains(line, want) {
			count++
		}
	}
	return count
}
