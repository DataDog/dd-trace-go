// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package utils

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tinylib/msgp/msgp"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	logger "github.com/DataDog/dd-trace-go/v2/internal/log"
)

type (
	// TestOptimizationMode stores the process-level mode settings for Bazel-compatible Test Optimization flows.
	TestOptimizationMode struct {
		ManifestEnabled     bool
		PayloadFilesEnabled bool
		ManifestPath        string
		ManifestDir         string
		PayloadsRoot        string
	}
)

var (
	testOptimizationModeMu   sync.Mutex
	testOptimizationModeOnce sync.Once
	currentTestOptimization  TestOptimizationMode
	payloadFileCounter       uint64 = 0
)

// CurrentTestOptimizationMode returns the resolved process-wide Test Optimization mode.
func CurrentTestOptimizationMode() TestOptimizationMode {
	testOptimizationModeMu.Lock()
	defer testOptimizationModeMu.Unlock()

	testOptimizationModeOnce.Do(func() {
		currentTestOptimization = resolveTestOptimizationMode()
	})

	return currentTestOptimization
}

// IsManifestModeEnabled returns true when a compatible manifest has been resolved.
func IsManifestModeEnabled() bool {
	return CurrentTestOptimizationMode().ManifestEnabled
}

// IsPayloadFilesModeEnabled returns true when payload-file mode is enabled through environment variables.
func IsPayloadFilesModeEnabled() bool {
	return CurrentTestOptimizationMode().PayloadFilesEnabled
}

// CacheHTTPFile returns the expected cache/http file path in manifest mode.
func CacheHTTPFile(name string) (string, bool) {
	mode := CurrentTestOptimizationMode()
	if !mode.ManifestEnabled || strings.TrimSpace(name) == "" {
		return "", false
	}
	return filepath.Join(mode.ManifestDir, "cache", "http", name), true
}

// MsgpackToJSON converts any MessagePack payload into JSON bytes.
func MsgpackToJSON(msgpackPayload []byte) ([]byte, error) {
	if len(msgpackPayload) == 0 {
		return nil, errors.New("msgpack payload is empty")
	}

	var jsonBuf bytes.Buffer
	if _, err := msgp.CopyToJSON(&jsonBuf, bytes.NewReader(msgpackPayload)); err != nil {
		return nil, fmt.Errorf("converting msgpack to json: %w", err)
	}
	return jsonBuf.Bytes(), nil
}

// WritePayloadFile writes payload JSON in Bazel undeclared outputs.
func WritePayloadFile(kind string, jsonPayload []byte) error {
	if kind != "tests" && kind != "coverage" {
		return fmt.Errorf("unsupported payload file kind %q", kind)
	}

	mode := CurrentTestOptimizationMode()
	if !mode.PayloadFilesEnabled {
		return errors.New("payload file mode is disabled")
	}
	if mode.PayloadsRoot == "" {
		return fmt.Errorf("%s is required when %s is enabled", constants.CIVisibilityUndeclaredOutputsDir, constants.CIVisibilityPayloadsInFiles)
	}

	outDir := filepath.Join(mode.PayloadsRoot, kind)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating payload output dir: %w", err)
	}

	seq := atomic.AddUint64(&payloadFileCounter, 1)
	fileName := fmt.Sprintf("%s-%d-%d-%d.json", kind, time.Now().UnixNano(), os.Getpid(), seq)
	filePath := filepath.Join(outDir, fileName)

	if err := os.WriteFile(filePath, jsonPayload, 0o644); err != nil {
		return fmt.Errorf("writing payload file: %w", err)
	}
	logger.Debug("civisibility: wrote %s payload file: %s", kind, filePath)
	return nil
}

func resolveTestOptimizationMode() TestOptimizationMode {
	mode := TestOptimizationMode{}

	manifestRloc := strings.TrimSpace(env.Get(constants.CIVisibilityManifestFilePath))
	if manifestRloc != "" {
		if manifestPath, ok := resolveManifestPath(manifestRloc); ok {
			mode.ManifestPath = manifestPath
			mode.ManifestDir = filepath.Dir(manifestPath)
			mode.ManifestEnabled = isManifestVersionSupported(manifestPath)
		}
	}

	mode.PayloadFilesEnabled = parseBoolEnv(strings.TrimSpace(env.Get(constants.CIVisibilityPayloadsInFiles)))
	if mode.PayloadFilesEnabled {
		if outputsDir := strings.TrimSpace(env.Get(constants.CIVisibilityUndeclaredOutputsDir)); outputsDir != "" {
			mode.PayloadsRoot = filepath.Join(outputsDir, "payloads")
		}
	}

	logger.Debug("civisibility: test optimization mode resolved [manifest_enabled:%t payload_files_enabled:%t manifest:%s payload_root:%s]",
		mode.ManifestEnabled, mode.PayloadFilesEnabled, mode.ManifestPath, mode.PayloadsRoot)
	return mode
}

func parseBoolEnv(raw string) bool {
	parsed, err := strconv.ParseBool(raw)
	return err == nil && parsed
}

func resolveManifestPath(p string) (string, bool) {
	if normalized, ok := existingFilePath(p); ok {
		return normalized, true
	}

	if runfilesDir := strings.TrimSpace(env.Get("RUNFILES_DIR")); runfilesDir != "" {
		if normalized, ok := existingFilePath(filepath.Join(runfilesDir, p)); ok {
			return normalized, true
		}
	}

	if runfilesManifest := strings.TrimSpace(env.Get("RUNFILES_MANIFEST_FILE")); runfilesManifest != "" {
		if candidate, ok := resolveRunfilesManifestEntry(runfilesManifest, p); ok {
			if normalized, exists := existingFilePath(candidate); exists {
				return normalized, true
			}
		}
	}

	if testSrcDir := strings.TrimSpace(env.Get("TEST_SRCDIR")); testSrcDir != "" {
		if normalized, ok := existingFilePath(filepath.Join(testSrcDir, p)); ok {
			return normalized, true
		}
	}

	return "", false
}

func existingFilePath(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path, true
	}
	return abs, true
}

func resolveRunfilesManifestEntry(manifestFilePath string, rlocation string) (string, bool) {
	file, err := os.Open(manifestFilePath)
	if err != nil {
		return "", false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		splitAt := strings.IndexByte(line, ' ')
		if splitAt <= 0 {
			continue
		}
		if line[:splitAt] == rlocation {
			return strings.TrimSpace(line[splitAt+1:]), true
		}
	}
	return "", false
}

func isManifestVersionSupported(manifestPath string) bool {
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		return false
	}

	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		version := strings.TrimSpace(scanner.Text())
		if version == "" {
			continue
		}
		return version == "1"
	}
	return false
}

// ResetTestOptimizationModeForTesting resets cached mode state.
// This helper is intended for tests that set per-test environment combinations.
func ResetTestOptimizationModeForTesting() {
	testOptimizationModeMu.Lock()
	defer testOptimizationModeMu.Unlock()
	testOptimizationModeOnce = sync.Once{}
	currentTestOptimization = TestOptimizationMode{}
	atomic.StoreUint64(&payloadFileCounter, 0)
}
