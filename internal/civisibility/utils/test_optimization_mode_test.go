// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tinylib/msgp/msgp"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
)

func TestCurrentTestOptimizationMode_DirectManifestPath(t *testing.T) {
	ResetTestOptimizationModeForTesting()
	t.Cleanup(ResetTestOptimizationModeForTesting)

	tmpDir := t.TempDir()
	manifestPath := filepath.Join(tmpDir, "manifest.txt")
	if err := os.WriteFile(manifestPath, []byte("1\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	t.Setenv(constants.CIVisibilityManifestFilePath, manifestPath)

	mode := CurrentTestOptimizationMode()
	if !mode.ManifestEnabled {
		t.Fatalf("expected manifest mode enabled")
	}
	if mode.ManifestPath != manifestPath {
		t.Fatalf("unexpected manifest path: %s", mode.ManifestPath)
	}
}

func TestCurrentTestOptimizationMode_RunfilesDirResolution(t *testing.T) {
	ResetTestOptimizationModeForTesting()
	t.Cleanup(ResetTestOptimizationModeForTesting)

	runfilesDir := t.TempDir()
	manifestRel := filepath.Join("workspace", ".testoptimization", "manifest.txt")
	manifestPath := filepath.Join(runfilesDir, manifestRel)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatalf("mkdir manifest dir: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte("1\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	t.Setenv(constants.CIVisibilityManifestFilePath, manifestRel)
	t.Setenv("RUNFILES_DIR", runfilesDir)

	mode := CurrentTestOptimizationMode()
	if !mode.ManifestEnabled {
		t.Fatalf("expected manifest mode enabled")
	}
	if mode.ManifestPath != manifestPath {
		t.Fatalf("unexpected manifest path: %s", mode.ManifestPath)
	}
}

func TestCurrentTestOptimizationMode_RunfilesManifestResolution(t *testing.T) {
	ResetTestOptimizationModeForTesting()
	t.Cleanup(ResetTestOptimizationModeForTesting)

	manifestPath := filepath.Join(t.TempDir(), "manifest.txt")
	if err := os.WriteFile(manifestPath, []byte("1\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	manifestRel := "workspace/.testoptimization/manifest.txt"
	runfilesManifest := filepath.Join(t.TempDir(), "MANIFEST")
	if err := os.WriteFile(runfilesManifest, []byte(manifestRel+" "+manifestPath+"\n"), 0o644); err != nil {
		t.Fatalf("write runfiles manifest: %v", err)
	}

	t.Setenv(constants.CIVisibilityManifestFilePath, manifestRel)
	t.Setenv("RUNFILES_MANIFEST_FILE", runfilesManifest)

	mode := CurrentTestOptimizationMode()
	if !mode.ManifestEnabled {
		t.Fatalf("expected manifest mode enabled")
	}
	if mode.ManifestPath != manifestPath {
		t.Fatalf("unexpected manifest path: %s", mode.ManifestPath)
	}
}

func TestCurrentTestOptimizationMode_TestSrcDirResolution(t *testing.T) {
	ResetTestOptimizationModeForTesting()
	t.Cleanup(ResetTestOptimizationModeForTesting)

	testSrcDir := t.TempDir()
	manifestRel := filepath.Join("workspace", ".testoptimization", "manifest.txt")
	manifestPath := filepath.Join(testSrcDir, manifestRel)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatalf("mkdir manifest dir: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte("1\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	t.Setenv(constants.CIVisibilityManifestFilePath, manifestRel)
	t.Setenv("TEST_SRCDIR", testSrcDir)

	mode := CurrentTestOptimizationMode()
	if !mode.ManifestEnabled {
		t.Fatalf("expected manifest mode enabled")
	}
	if mode.ManifestPath != manifestPath {
		t.Fatalf("unexpected manifest path: %s", mode.ManifestPath)
	}
}

func TestCurrentTestOptimizationMode_InvalidManifestVersion(t *testing.T) {
	ResetTestOptimizationModeForTesting()
	t.Cleanup(ResetTestOptimizationModeForTesting)

	manifestPath := filepath.Join(t.TempDir(), "manifest.txt")
	if err := os.WriteFile(manifestPath, []byte("2\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	t.Setenv(constants.CIVisibilityManifestFilePath, manifestPath)

	mode := CurrentTestOptimizationMode()
	if mode.ManifestEnabled {
		t.Fatalf("expected manifest mode disabled for unsupported version")
	}
}

func TestCurrentTestOptimizationMode_MissingManifestDisablesMode(t *testing.T) {
	ResetTestOptimizationModeForTesting()
	t.Cleanup(ResetTestOptimizationModeForTesting)

	t.Setenv(constants.CIVisibilityManifestFilePath, filepath.Join(t.TempDir(), "missing-manifest.txt"))

	mode := CurrentTestOptimizationMode()
	if mode.ManifestEnabled {
		t.Fatalf("expected manifest mode disabled for missing manifest")
	}
}

func TestCurrentTestOptimizationMode_ManifestVersionUsesFirstNonEmptyLine(t *testing.T) {
	ResetTestOptimizationModeForTesting()
	t.Cleanup(ResetTestOptimizationModeForTesting)

	manifestPath := filepath.Join(t.TempDir(), "manifest.txt")
	if err := os.WriteFile(manifestPath, []byte("\n  \n1\n2\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	t.Setenv(constants.CIVisibilityManifestFilePath, manifestPath)

	mode := CurrentTestOptimizationMode()
	if !mode.ManifestEnabled {
		t.Fatalf("expected manifest mode enabled when first non-empty line is 1")
	}
}

func TestCurrentTestOptimizationMode_PayloadFiles(t *testing.T) {
	ResetTestOptimizationModeForTesting()
	t.Cleanup(ResetTestOptimizationModeForTesting)

	outDir := t.TempDir()
	t.Setenv(constants.CIVisibilityPayloadsInFiles, "true")
	t.Setenv(constants.CIVisibilityUndeclaredOutputsDir, outDir)

	mode := CurrentTestOptimizationMode()
	if !mode.PayloadFilesEnabled {
		t.Fatalf("expected payload-files mode enabled")
	}
	expectedRoot := filepath.Join(outDir, "payloads")
	if mode.PayloadsRoot != expectedRoot {
		t.Fatalf("unexpected payload root: %s", mode.PayloadsRoot)
	}

	if err := WritePayloadFile("tests", []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("write payload file: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(expectedRoot, "tests", "tests-*.json"))
	if err != nil {
		t.Fatalf("glob payload files: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one tests payload file, got %d", len(matches))
	}
}

func TestCacheHTTPFile(t *testing.T) {
	ResetTestOptimizationModeForTesting()
	t.Cleanup(ResetTestOptimizationModeForTesting)

	manifestDir := t.TempDir()
	manifestPath := filepath.Join(manifestDir, "manifest.txt")
	if err := os.WriteFile(manifestPath, []byte("1\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	t.Setenv(constants.CIVisibilityManifestFilePath, manifestPath)

	cacheFile, ok := CacheHTTPFile("settings.json")
	if !ok {
		t.Fatalf("expected cache file resolution to be enabled")
	}
	expected := filepath.Join(manifestDir, "cache", "http", "settings.json")
	if cacheFile != expected {
		t.Fatalf("unexpected cache file path: %s", cacheFile)
	}
}

func TestMsgpackToJSON(t *testing.T) {
	payload := msgp.AppendMapHeader(nil, 1)
	payload = msgp.AppendString(payload, "k")
	payload = msgp.AppendString(payload, "v")

	jsonPayload, err := MsgpackToJSON(payload)
	if err != nil {
		t.Fatalf("msgpack to json failed: %v", err)
	}
	if !strings.Contains(string(jsonPayload), "\"k\"") || !strings.Contains(string(jsonPayload), "\"v\"") {
		t.Fatalf("unexpected json payload: %s", string(jsonPayload))
	}
}
