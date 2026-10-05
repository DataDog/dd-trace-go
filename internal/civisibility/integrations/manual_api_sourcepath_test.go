// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sourcePathFixtureFunc is a real runtime function used to exercise SetTestFunc path resolution.
func sourcePathFixtureFunc() {
}

// sourcePathRepositoryRoot returns the repository root from the package test working directory.
func sourcePathRepositoryRoot(t *testing.T) string {
	t.Helper()

	workingDirectory, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Clean(filepath.Join(workingDirectory, "..", "..", ".."))
}

// configureSourcePathTestState resets process-global CI Visibility state and installs source tags.
func configureSourcePathTestState(t *testing.T, workspacePath string) {
	t.Helper()

	resetCIVisibilityStateForTesting()
	mockTracer.Reset()
	utils.AddCITagsMap(map[string]string{
		constants.CIWorkspacePath:  workspacePath,
		constants.GitRepositoryURL: "https://github.com/DataDog/dd-trace-go.git",
	})
	t.Cleanup(resetCIVisibilityStateForTesting)
}

// currentBinaryWasBuiltWithTrimpath reports whether the running test binary has -trimpath enabled.
func currentBinaryWasBuiltWithTrimpath(t *testing.T) bool {
	t.Helper()

	buildInfo, ok := debug.ReadBuildInfo()
	require.True(t, ok)
	for _, setting := range buildInfo.Settings {
		if setting.Key == "-trimpath" {
			return setting.Value == "true"
		}
	}
	return false
}

// assertSourceRangeTags verifies that SetTestFunc parsed source metadata and published a valid range.
func assertSourceRangeTags(t *testing.T, test Test) {
	t.Helper()

	startLine, startOK := test.GetTag(constants.TestSourceStartLine)
	endLine, endOK := test.GetTag(constants.TestSourceEndLine)
	require.True(t, startOK)
	require.True(t, endOK)
	require.IsType(t, float64(0), startLine)
	require.IsType(t, float64(0), endLine)
	assert.GreaterOrEqual(t, endLine.(float64), startLine.(float64))
}

func TestSetTestFuncUsesResolvedSourcePathForTagsAndParsing(t *testing.T) {
	configureSourcePathTestState(t, sourcePathRepositoryRoot(t))

	now := time.Now()
	session, module, suite, test := createDDTest(now)
	defer func() {
		session.Close(0)
		module.Close()
		suite.Close()
	}()

	fn := runtime.FuncForPC(reflect.ValueOf(sourcePathFixtureFunc).Pointer())
	require.NotNil(t, fn)
	runtimePath, _ := fn.FileLine(fn.Entry())
	expectedSourcePath := resolveTestSourcePath(runtimePath)

	test.SetTestFunc(fn)

	testSourceFile, testSourceFileOK := test.GetTag(constants.TestSourceFile)
	suiteSourceFile, suiteSourceFileOK := suite.GetTag(constants.TestSourceFile)
	require.True(t, testSourceFileOK)
	require.True(t, suiteSourceFileOK)
	assert.Equal(t, expectedSourcePath.RelativePath, testSourceFile)
	assert.Equal(t, expectedSourcePath.RelativePath, suiteSourceFile)
	assertSourceRangeTags(t, test)
}

func TestResolveTestSourcePathCanParseTrimpathModulePath(t *testing.T) {
	workspacePath := t.TempDir()
	configureSourcePathTestState(t, workspacePath)

	relativePath := filepath.Join("internal", "civisibility", "integrations", "manual_api_sourcepath_test.go")
	sourceFilePath := filepath.Join(workspacePath, relativePath)
	require.NoError(t, os.MkdirAll(filepath.Dir(sourceFilePath), 0o700))
	require.NoError(t, os.WriteFile(sourceFilePath, []byte("package integrations\n\nfunc sourcePathTemporaryFixture() {\n}\n"), 0o600))

	sourcePath := resolveTestSourcePath("github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/manual_api_sourcepath_test.go")
	metadata := loadSourceFileMetadata(sourcePath.FilesystemPath)

	assert.Equal(t, "internal/civisibility/integrations/manual_api_sourcepath_test.go", sourcePath.RelativePath)
	assert.Equal(t, sourceFilePath, sourcePath.FilesystemPath)
	assert.True(t, sourcePath.FilesystemKnown)
	require.True(t, metadata.parseOK, "expected %q to parse: %v", sourcePath.FilesystemPath, metadata.parseErr)
	assert.Contains(t, metadata.namedFunctions, "sourcePathTemporaryFixture")
}

func TestSetTestFuncUnderTrimpathUsesRepositoryRelativeTagsAndParsesSource(t *testing.T) {
	if !currentBinaryWasBuiltWithTrimpath(t) {
		t.Skip("this regression is meaningful only when the test binary is built with -trimpath")
	}
	configureSourcePathTestState(t, sourcePathRepositoryRoot(t))

	now := time.Now()
	session, module, suite, test := createDDTest(now)
	defer func() {
		session.Close(0)
		module.Close()
		suite.Close()
	}()

	fn := runtime.FuncForPC(reflect.ValueOf(sourcePathFixtureFunc).Pointer())
	require.NotNil(t, fn)
	runtimePath, _ := fn.FileLine(fn.Entry())
	require.False(t, filepath.IsAbs(runtimePath), "go test -trimpath should not report an absolute runtime source path")
	require.True(t, strings.HasPrefix(runtimePath, "github.com/DataDog/dd-trace-go/v2/"), "unexpected trimpath runtime source path: %s", runtimePath)

	test.SetTestFunc(fn)

	testSourceFile, ok := test.GetTag(constants.TestSourceFile)
	require.True(t, ok)
	assert.Equal(t, "internal/civisibility/integrations/manual_api_sourcepath_test.go", testSourceFile)
	assert.NotContains(t, testSourceFile, "github.com/DataDog/dd-trace-go")
	assert.NotContains(t, testSourceFile, "v2/internal")
	assertSourceRangeTags(t, test)
}
