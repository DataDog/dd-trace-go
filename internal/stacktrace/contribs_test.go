// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stacktrace

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/mod/modfile"
)

// TestExtractThirdPartyLibraries tests the core logic for extracting third-party
// libraries from contrib go.mod files
func TestExtractThirdPartyLibraries(t *testing.T) {
	tests := []struct {
		name         string
		goModContent string
		expected     []string
	}{
		{
			name: "gorilla/mux pattern",
			goModContent: `module github.com/DataDog/dd-trace-go/contrib/gorilla/mux/v2

go 1.24.0

require (
	github.com/DataDog/dd-trace-go/v2 v2.3.0-dev.1
	github.com/gorilla/mux v1.8.1
	github.com/stretchr/testify v1.10.0
)`,
			expected: []string{"github.com/gorilla/mux"},
		},
		{
			name: "gin-gonic/gin pattern",
			goModContent: `module github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin/v2

go 1.24.0

require (
	github.com/DataDog/dd-trace-go/v2 v2.3.0-dev.1
	github.com/gin-gonic/gin v1.10.1
	github.com/stretchr/testify v1.10.0
)`,
			expected: []string{"github.com/gin-gonic/gin"},
		},
		{
			name: "cloud.google.com pattern",
			goModContent: `module github.com/DataDog/dd-trace-go/contrib/cloud.google.com/go/pubsub.v1/v2

go 1.24.0

require (
	cloud.google.com/go/pubsub v1.37.0
	github.com/DataDog/dd-trace-go/v2 v2.3.0-dev.1
	github.com/stretchr/testify v1.10.0
)`,
			expected: []string{"cloud.google.com/go/pubsub"},
		},
		{
			name: "redis pattern - multiple versions",
			goModContent: `module github.com/DataDog/dd-trace-go/contrib/go-redis/redis/v2

go 1.24.0

require (
	github.com/DataDog/dd-trace-go/v2 v2.3.0-dev.1
	github.com/go-redis/redis v6.15.9+incompatible
	github.com/stretchr/testify v1.10.0
)`,
			expected: []string{"github.com/go-redis/redis"},
		},
		{
			name: "no third-party deps - only DataDog and testify",
			goModContent: `module github.com/DataDog/dd-trace-go/contrib/internal/test/v2

go 1.24.0

require (
	github.com/DataDog/dd-trace-go/v2 v2.3.0-dev.1
	github.com/stretchr/testify v1.10.0
)`,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractThirdPartyLibraries(tt.goModContent)
			require.Equal(t, tt.expected, result)
		})
	}
}

// TestScanContribDirectories tests scanning actual contrib directories
func TestScanContribDirectories(t *testing.T) {
	// Get the project root by walking up from current directory
	projectRoot, err := findProjectRoot()
	require.NoError(t, err)

	contribDir := filepath.Join(projectRoot, "contrib")

	// Verify contrib directory exists
	_, err = os.Stat(contribDir)
	require.NoError(t, err, "contrib directory should exist")

	// Scan for all go.mod files in contrib
	goModFiles, err := scanContribGoMods(contribDir)
	require.NoError(t, err)
	require.Greater(t, len(goModFiles), 50, "should find many contrib go.mod files")

	// Extract third-party libraries from all contrib modules
	libraries, err := extractAllThirdPartyLibraries(contribDir)
	require.NoError(t, err)
	require.Greater(t, len(libraries), 100, "should extract many third-party libraries")

	// Verify some expected libraries are present
	expectedLibraries := []string{
		"github.com/gorilla/mux",
		"github.com/gin-gonic/gin",
		"cloud.google.com/go/pubsub",
		"github.com/go-redis/redis",
	}

	for _, expected := range expectedLibraries {
		require.Contains(t, libraries, expected, "should contain %s", expected)
	}

	// Verify no DataDog internal libraries are included
	for _, lib := range libraries {
		require.False(t, strings.HasPrefix(lib, "github.com/DataDog/"),
			"should not contain DataDog internal library: %s", lib)
		require.False(t, strings.HasPrefix(lib, "github.com/stretchr/testify"),
			"should not contain testify: %s", lib)
	}
}

// TestGeneratedThirdPartyLibrariesConsistency tests that the generated function
// produces results consistent with current hardcoded list
func TestGeneratedThirdPartyLibrariesConsistency(t *testing.T) {
	projectRoot, err := findProjectRoot()
	require.NoError(t, err)

	contribDir := filepath.Join(projectRoot, "contrib")
	generated, err := extractAllThirdPartyLibraries(contribDir)
	require.NoError(t, err)

	// Compare with current hardcoded list
	current := knownThirdPartyLibraries

	// Generated list should contain most of the current hardcoded entries
	// (allowing for some differences due to contrib structure evolution)
	overlapping := 0
	for _, currentLib := range current {
		for _, genLib := range generated {
			if strings.HasPrefix(genLib, currentLib) || strings.HasPrefix(currentLib, genLib) {
				overlapping++
				break
			}
		}
	}

	// At least 70% should overlap (allowing for evolution)
	overlapPercentage := float64(overlapping) / float64(len(current))
	require.Greater(t, overlapPercentage, 0.7,
		"generated list should have significant overlap with current hardcoded list")
}

// TestClassifySymbolWithGeneratedLibraries tests that symbol classification
// works correctly with generated libraries
func TestClassifySymbolWithGeneratedLibraries(t *testing.T) {
	projectRoot, err := findProjectRoot()
	require.NoError(t, err)

	contribDir := filepath.Join(projectRoot, "contrib")
	generated, err := extractAllThirdPartyLibraries(contribDir)
	require.NoError(t, err)

	tests := []struct {
		name     string
		symbol   symbol
		expected frameType
	}{
		{
			name:     "datadog internal",
			symbol:   symbol{Package: "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"},
			expected: frameTypeDatadog,
		},
		{
			name:     "standard library",
			symbol:   symbol{Package: "net/http"},
			expected: frameTypeRuntime,
		},
		{
			name:     "customer code",
			symbol:   symbol{Package: "github.com/customer/app"},
			expected: frameTypeCustomer,
		},
	}

	// Add tests for generated third-party libraries
	for i, lib := range generated {
		if i >= 5 { // Test first 5 to keep test fast
			break
		}
		tests = append(tests, struct {
			name     string
			symbol   symbol
			expected frameType
		}{
			name:     "generated third-party: " + lib,
			symbol:   symbol{Package: lib},
			expected: frameTypeThirdParty,
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test with generated libraries instead of hardcoded
			result := classifySymbolWithLibraries(tt.symbol, internalSymbolPrefixes, generated)
			require.Equal(t, tt.expected, result)
		})
	}
}

// Helper functions that mirror the generation tool logic

func extractThirdPartyLibraries(goModContent string) []string {
	modFile, err := modfile.Parse("go.mod", []byte(goModContent), nil)
	if err != nil {
		return nil
	}

	var thirdPartyLibs []string
	for _, req := range modFile.Require {
		path := req.Mod.Path

		// Skip DataDog internal dependencies
		if strings.HasPrefix(path, "github.com/DataDog/") {
			continue
		}

		// Skip testing dependencies
		if strings.HasPrefix(path, "github.com/stretchr/testify") {
			continue
		}

		// Skip golang.org dependencies (usually tooling)
		if strings.HasPrefix(path, "golang.org/") {
			continue
		}

		// Include all other third-party libraries to maximize coverage
		thirdPartyLibs = append(thirdPartyLibs, path)
	}

	return thirdPartyLibs
}

func scanContribGoMods(contribDir string) ([]string, error) {
	var goModFiles []string

	err := filepath.Walk(contribDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.Name() == "go.mod" {
			goModFiles = append(goModFiles, path)
		}

		return nil
	})

	return goModFiles, err
}

func extractAllThirdPartyLibraries(contribDir string) ([]string, error) {
	goModFiles, err := scanContribGoMods(contribDir)
	if err != nil {
		return nil, err
	}

	var allLibraries []string
	seen := make(map[string]bool)

	for _, goModPath := range goModFiles {
		content, err := os.ReadFile(goModPath)
		if err != nil {
			continue // Skip files we can't read
		}

		libs := extractThirdPartyLibraries(string(content))
		for _, lib := range libs {
			if !seen[lib] {
				seen[lib] = true
				allLibraries = append(allLibraries, lib)
			}
		}
	}

	sort.Strings(allLibraries)
	return allLibraries, nil
}

func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		// Look for go.work file (workspace root) or main go.mod
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, nil
		}

		// Check if we're in the main dd-trace-go directory by looking for contrib/
		if _, err := os.Stat(filepath.Join(dir, "contrib")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "internal", "stacktrace")); err == nil {
				return dir, nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break // Reached root
		}
		dir = parent
	}

	return "", os.ErrNotExist
}

// classifySymbolWithLibraries is a test helper that uses provided libraries
// instead of the global knownThirdPartyLibraries
func classifySymbolWithLibraries(sym symbol, internalPrefixes []string, thirdPartyLibs []string) frameType {
	pkg := sym.Package

	for _, prefix := range internalPrefixes {
		if strings.HasPrefix(pkg, prefix) {
			return frameTypeDatadog
		}
	}

	if isStandardLibraryPackage(pkg) {
		return frameTypeRuntime
	}

	for _, lib := range thirdPartyLibs {
		if strings.HasPrefix(pkg, lib) {
			return frameTypeThirdParty
		}
	}

	return frameTypeCustomer
}
