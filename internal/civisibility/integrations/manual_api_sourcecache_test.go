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
	"sync/atomic"
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

func secondNamedSourceFixtureFunc() {}

func namedSourceFixtureRuntimeFunc() *runtime.Func {
	return runtime.FuncForPC(reflect.ValueOf(namedSourceFixtureFunc).Pointer())
}

func secondNamedSourceFixtureRuntimeFunc() *runtime.Func {
	return runtime.FuncForPC(reflect.ValueOf(secondNamedSourceFixtureFunc).Pointer())
}

func literalSourceFixtureRuntimeFunc() *runtime.Func {
	literal := func() {}
	return runtime.FuncForPC(reflect.ValueOf(literal).Pointer())
}

type recordingImpactedTestClassifier struct {
	called    bool
	testName  string
	source    string
	startLine int
	endLine   int
	result    bool
}

func (c *recordingImpactedTestClassifier) IsImpacted(testName, source string, startLine, endLine int) bool {
	c.called = true
	c.testName = testName
	c.source = source
	c.startLine = startLine
	c.endLine = endLine
	return c.result
}

func TestIsTestFuncModifiedUsesResolvedSourceRange(t *testing.T) {
	resetSourceCacheTestState(t)
	classifier := &recordingImpactedTestClassifier{result: true}

	require.True(t, isTestFuncModified(classifier, "TestSourceFixture", namedSourceFixtureRuntimeFunc()))
	require.True(t, classifier.called)
	require.Equal(t, "TestSourceFixture", classifier.testName)
	require.NotEmpty(t, classifier.source)
	require.Positive(t, classifier.startLine)
	require.GreaterOrEqual(t, classifier.endLine, classifier.startLine)
}

func TestIsTestFuncModifiedWithAnalyzerRejectsMissingAnalyzer(t *testing.T) {
	assert.False(t, IsTestFuncModifiedWithAnalyzer(nil, "TestSourceFixture", namedSourceFixtureRuntimeFunc()))
}

func TestTestFuncSourceMetadataDoesNotRequireTestEvent(t *testing.T) {
	resetSourceCacheTestState(t)
	start, end, unskippable := TestFuncSourceMetadata(runtime.FuncForPC(reflect.ValueOf(suiteUnskippableFixtureFunc).Pointer()))
	assert.Positive(t, start)
	assert.GreaterOrEqual(t, end, start)
	assert.True(t, unskippable)
	_, _, unskippable = TestFuncSourceMetadata(runtime.FuncForPC(reflect.ValueOf(declarationUnskippableFixtureFunc).Pointer()))
	assert.True(t, unskippable)
	_, _, unskippable = TestFuncSourceMetadata(namedSourceFixtureRuntimeFunc())
	assert.False(t, unskippable)
	start, end, unskippable = TestFuncSourceMetadata(nil)
	assert.Zero(t, start)
	assert.Zero(t, end)
	assert.False(t, unskippable)
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

func TestLoadSourceFunctionMetadataCachesResolvedFunction(t *testing.T) {
	resetSourceCacheTestState(t)

	fn := namedSourceFixtureRuntimeFunc()
	first := loadSourceFunctionMetadata(fn)
	second := loadSourceFunctionMetadata(fn)

	require.Equal(t, first.runtimePath, second.runtimePath)
	require.Equal(t, first.runtimeStartLine, second.runtimeStartLine)
	require.Equal(t, first.sourcePath, second.sourcePath)
	require.Equal(t, first.resolution, second.resolution)
	firstOwner, firstOwnerFound := loadSourceFunctionCodeOwner(first)
	secondOwner, secondOwnerFound := loadSourceFunctionCodeOwner(second)
	require.True(t, firstOwnerFound)
	require.Equal(t, firstOwnerFound, secondOwnerFound)
	require.Equal(t, firstOwner, secondOwner)
	require.NotEmpty(t, firstOwner)

	entries := 0
	sourceFunctionMetadataCache.Range(func(key, value any) bool {
		entries++
		require.Equal(t, fn.Entry(), key)
		slot, ok := value.(*sourceFunctionCacheSlot)
		require.True(t, ok)
		require.Equal(t, first, slot.entry)
		return true
	})
	require.Equal(t, 1, entries)
}

type recordingCodeOwnerMatcher struct {
	calls int
	entry utils.Entry
}

func (m *recordingCodeOwnerMatcher) Match(string) (*utils.Entry, bool) {
	m.calls++
	return &m.entry, true
}

type staticCodeOwnerMatcher struct {
	entry utils.Entry
}

func (m *staticCodeOwnerMatcher) Match(string) (*utils.Entry, bool) {
	return &m.entry, true
}

func TestSourceFileCodeOwnerCacheIsSharedAcrossFunctions(t *testing.T) {
	resetSourceCacheTestState(t)

	first := loadSourceFunctionMetadata(namedSourceFixtureRuntimeFunc())
	second := loadSourceFunctionMetadata(secondNamedSourceFixtureRuntimeFunc())
	require.Equal(t, first.sourcePath.FilesystemPath, second.sourcePath.FilesystemPath)

	firstSlot := sourceFileSlot(first.sourcePath.FilesystemPath, newSourceFileCacheSlot)
	secondSlot := sourceFileSlot(second.sourcePath.FilesystemPath, newSourceFileCacheSlot)
	require.Same(t, firstSlot, secondSlot)

	matcher := &recordingCodeOwnerMatcher{entry: utils.Entry{Owners: []string{"@shared-owner"}}}
	firstOwner, firstFound := loadSourceFileCodeOwner(firstSlot, matcher, true, first.sourcePath.RelativePath)
	secondOwner, secondFound := loadSourceFileCodeOwner(secondSlot, matcher, true, second.sourcePath.RelativePath)

	require.True(t, firstFound)
	require.True(t, secondFound)
	require.Equal(t, "[\"@shared-owner\"]", firstOwner)
	require.Equal(t, firstOwner, secondOwner)
	require.Equal(t, 1, matcher.calls)
}

func TestSourceFileCodeOwnerCacheCachesCompletedMissingLookup(t *testing.T) {
	slot := &sourceFileCacheSlot{}
	matcher := &recordingCodeOwnerMatcher{entry: utils.Entry{Owners: []string{"@unexpected-owner"}}}

	firstOwner, firstFound := loadSourceFileCodeOwner(slot, nil, true, "source.go")
	secondOwner, secondFound := loadSourceFileCodeOwner(slot, matcher, true, "source.go")

	require.False(t, firstFound)
	require.False(t, secondFound)
	require.Empty(t, firstOwner)
	require.Empty(t, secondOwner)
	require.Zero(t, matcher.calls)
}

func TestSourceFileCodeOwnerCacheRetriesIncompleteLookup(t *testing.T) {
	slot := &sourceFileCacheSlot{}
	matcher := &recordingCodeOwnerMatcher{entry: utils.Entry{Owners: []string{"@recovered-owner"}}}

	firstOwner, firstFound := loadSourceFileCodeOwner(slot, nil, false, "source.go")
	secondOwner, secondFound := loadSourceFileCodeOwner(slot, matcher, true, "source.go")

	require.False(t, firstFound)
	require.Empty(t, firstOwner)
	require.True(t, secondFound)
	require.Equal(t, "[\"@recovered-owner\"]", secondOwner)
	require.Equal(t, 1, matcher.calls)
}

func TestSourceFunctionCodeOwnerCacheSkipsCompletedLookup(t *testing.T) {
	slot := &sourceFileCacheSlot{}
	metadata := sourceFunctionMetadata{
		fileSlot:   slot,
		sourcePath: utils.SourceFilePath{RelativePath: "source.go"},
	}
	matcher := &recordingCodeOwnerMatcher{entry: utils.Entry{Owners: []string{"@cached-owner"}}}
	lookups := 0
	lookup := func() (codeOwnerMatcher, bool) {
		lookups++
		return matcher, true
	}

	firstOwner, firstFound := loadSourceFunctionCodeOwnerWithLookup(metadata, lookup)
	secondOwner, secondFound := loadSourceFunctionCodeOwnerWithLookup(metadata, lookup)

	require.True(t, firstFound)
	require.True(t, secondFound)
	require.Equal(t, "[\"@cached-owner\"]", firstOwner)
	require.Equal(t, firstOwner, secondOwner)
	require.Equal(t, 1, lookups)
	require.Equal(t, 1, matcher.calls)
}

func TestSourceFunctionCodeOwnerCacheConcurrentHitsSkipCompletedLookup(t *testing.T) {
	slot := &sourceFileCacheSlot{}
	metadata := sourceFunctionMetadata{
		fileSlot:   slot,
		sourcePath: utils.SourceFilePath{RelativePath: "source.go"},
	}
	matcher := &staticCodeOwnerMatcher{entry: utils.Entry{Owners: []string{"@cached-owner"}}}
	var lookups atomic.Int64
	lookup := func() (codeOwnerMatcher, bool) {
		lookups.Add(1)
		return matcher, true
	}

	owner, found := loadSourceFunctionCodeOwnerWithLookup(metadata, lookup)
	require.True(t, found)
	require.Equal(t, "[\"@cached-owner\"]", owner)

	const readers = 32
	var wait sync.WaitGroup
	wait.Add(readers)
	owners := make([]string, readers)
	foundResults := make([]bool, readers)
	for index := range readers {
		go func() {
			defer wait.Done()
			owners[index], foundResults[index] = loadSourceFunctionCodeOwnerWithLookup(metadata, lookup)
		}()
	}
	wait.Wait()

	for index := range readers {
		require.True(t, foundResults[index])
		require.Equal(t, owner, owners[index])
	}
	require.Equal(t, int64(1), lookups.Load())
}

func BenchmarkSourceFileCodeOwnerSharedMiss(b *testing.B) {
	matcher := &recordingCodeOwnerMatcher{entry: utils.Entry{Owners: []string{"@shared-owner"}}}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		slot := &sourceFileCacheSlot{}
		loadSourceFileCodeOwner(slot, matcher, true, "source.go")
		loadSourceFileCodeOwner(slot, matcher, true, "source.go")
	}
	b.StopTimer()
	if matcher.calls != b.N {
		b.Fatalf("CODEOWNERS matcher calls = %d, want %d", matcher.calls, b.N)
	}
}

func BenchmarkSourceFileCodeOwnerCachedResult(b *testing.B) {
	slot := &sourceFileCacheSlot{}
	matcher := &recordingCodeOwnerMatcher{entry: utils.Entry{Owners: []string{"@shared-owner"}}}
	loadSourceFileCodeOwner(slot, matcher, true, "source.go")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		loadSourceFileCodeOwner(slot, matcher, true, "source.go")
	}
	b.StopTimer()
	if matcher.calls != 1 {
		b.Fatalf("CODEOWNERS matcher calls = %d, want 1", matcher.calls)
	}
}

func BenchmarkSourceFunctionCodeOwnerCachedResult(b *testing.B) {
	slot := &sourceFileCacheSlot{}
	metadata := sourceFunctionMetadata{
		fileSlot:   slot,
		sourcePath: utils.SourceFilePath{RelativePath: "source.go"},
	}
	matcher := &recordingCodeOwnerMatcher{entry: utils.Entry{Owners: []string{"@shared-owner"}}}
	lookups := 0
	lookup := func() (codeOwnerMatcher, bool) {
		lookups++
		return matcher, true
	}
	loadSourceFunctionCodeOwnerWithLookup(metadata, lookup)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		loadSourceFunctionCodeOwnerWithLookup(metadata, lookup)
	}
	b.StopTimer()
	if lookups != 1 {
		b.Fatalf("CODEOWNERS lookups = %d, want 1", lookups)
	}
}

func TestSourceFunctionCacheHitDoesNotCreateDiscardedSlot(t *testing.T) {
	resetSourceCacheTestState(t)
	created := 0
	newSlot := func() *sourceFunctionCacheSlot {
		created++
		return &sourceFunctionCacheSlot{}
	}

	fn := namedSourceFixtureRuntimeFunc()
	first := sourceFunctionSlotForEntry(fn.Entry(), newSlot)
	second := sourceFunctionSlotForEntry(fn.Entry(), newSlot)

	require.Same(t, first, second)
	require.Equal(t, 1, created)
}

func TestSourceFileCacheHitDoesNotCreateDiscardedSlot(t *testing.T) {
	resetSourceCacheTestState(t)
	created := 0
	newSlot := func() *sourceFileCacheSlot {
		created++
		return &sourceFileCacheSlot{}
	}

	path := filepath.Join(t.TempDir(), "missing.go")
	first := sourceFileSlot(path, newSlot)
	second := sourceFileSlot(path, newSlot)

	require.Same(t, first, second)
	require.Equal(t, 1, created)
}

func TestFunctionLiteralsToLogSkipsDebugOnlyTraversal(t *testing.T) {
	literals := []functionLiteralMetadata{{bodyStartLine: 10, endLine: 12}}

	require.NotPanics(t, func() {
		require.Nil(t, functionLiteralsToLog(literals, len(literals)+1, false))
	})
	require.Equal(t, literals, functionLiteralsToLog(literals, len(literals), true))
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

	literal, inspectedLiteralCount, ok := findMatchingFunctionLiteral(literals, 19)
	require.True(t, ok)
	assert.Equal(t, 20, literal.bodyStartLine)
	assert.Equal(t, len(literals), inspectedLiteralCount)

	literal, inspectedLiteralCount, ok = findMatchingFunctionLiteral([]functionLiteralMetadata{
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
	assert.Equal(t, 2, inspectedLiteralCount)

	literal, inspectedLiteralCount, ok = findMatchingFunctionLiteral(literals, 10)
	require.True(t, ok)
	assert.Equal(t, 10, literal.bodyStartLine)
	assert.Equal(t, 1, inspectedLiteralCount)

	_, inspectedLiteralCount, ok = findMatchingFunctionLiteral(literals, 50)
	assert.False(t, ok)
	assert.Equal(t, len(literals), inspectedLiteralCount)
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
	assert.Zero(t, resolution.inspectedLiteralCount)
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
	assert.Equal(t, 1, resolution.inspectedLiteralCount)
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
	assert.Equal(t, 2, resolution.inspectedLiteralCount)
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
	assert.Equal(t, 1, resolution.inspectedLiteralCount)
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
	assert.Equal(t, 1, resolution.inspectedLiteralCount)
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
	assert.Zero(t, resolution.inspectedLiteralCount)
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
	assert.Equal(t, 1, resolution.inspectedLiteralCount)
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
	assert.Equal(t, 1, resolution.inspectedLiteralCount)
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
