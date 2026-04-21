// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package bazel contains Bazel-specific compatibility helpers used by CI Visibility
// and other payload-file based flows.
package bazel

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

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	logger "github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	// ManifestFilePathEnv points to the runfiles manifest or manifest rlocation used by offline Bazel mode.
	ManifestFilePathEnv = "DD_TEST_OPTIMIZATION_MANIFEST_FILE"
	// PayloadsInFilesEnv enables writing CI Visibility and telemetry payloads to undeclared output files.
	PayloadsInFilesEnv = "DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES"
	// UndeclaredOutputsDirEnv points to Bazel's undeclared outputs root where payload files are stored.
	UndeclaredOutputsDirEnv = "TEST_UNDECLARED_OUTPUTS_DIR"
)

// PayloadKind identifies the payload subdirectory and naming strategy used for
// Bazel payload-file mode.
type PayloadKind string

const (
	// PayloadKindTests writes CI Visibility test event payloads.
	PayloadKindTests PayloadKind = "tests"
	// PayloadKindCoverage writes CI Visibility coverage payloads.
	PayloadKindCoverage PayloadKind = "coverage"
	// PayloadKindTelemetry writes raw top-level telemetry request bodies.
	PayloadKindTelemetry PayloadKind = "telemetry"
)

// Mode stores the process-level Bazel compatibility settings.
type Mode struct {
	// ManifestEnabled reports whether a supported manifest was found and can be used for offline cache reads.
	ManifestEnabled bool
	// PayloadFilesEnabled reports whether Bazel payload-file mode is enabled.
	PayloadFilesEnabled bool
	// ManifestPath is the resolved absolute path to the Bazel manifest file when manifest mode is enabled.
	ManifestPath string
	// ManifestDir is the directory that contains the resolved manifest and its cache tree.
	ManifestDir string
	// PayloadsRoot is the root directory that contains per-kind payload output subdirectories.
	PayloadsRoot string
}

var (
	// modeMu protects the lazy resolution state so tests can safely reset it.
	modeMu sync.Mutex
	// modeOnce resolves the process-wide Bazel mode exactly once per environment configuration.
	modeOnce sync.Once
	// currentMode caches the resolved Bazel mode for the current process.
	currentMode Mode
	// payloadFileCount keeps payload filenames unique within a process and orders telemetry files deterministically.
	payloadFileCount uint64
)

// CurrentMode returns the resolved process-wide Bazel mode.
func CurrentMode() Mode {
	modeMu.Lock()
	defer modeMu.Unlock()

	modeOnce.Do(func() {
		currentMode = resolveMode()
	})

	return currentMode
}

// IsManifestModeEnabled returns true when a compatible manifest has been resolved.
func IsManifestModeEnabled() bool {
	return CurrentMode().ManifestEnabled
}

// IsPayloadFilesModeEnabled returns true when payload-file mode is enabled through environment variables.
func IsPayloadFilesModeEnabled() bool {
	return CurrentMode().PayloadFilesEnabled
}

// IsGitCLIDisabled returns true when the current Bazel mode must not invoke the Git CLI.
func IsGitCLIDisabled() bool {
	return CurrentMode().PayloadFilesEnabled
}

// CacheHTTPFile returns the expected cache/http file path in manifest mode.
func CacheHTTPFile(name string) (string, bool) {
	mode := CurrentMode()
	if !mode.ManifestEnabled || strings.TrimSpace(name) == "" {
		return "", false
	}
	cacheFile := filepath.Join(mode.ManifestDir, "cache", "http", name)
	logger.Debug("civisibility: cache file resolved [name:%s path:%s]", name, TestOptimizationPathForLog(cacheFile))
	return cacheFile, true
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

// WritePayloadFile writes a JSON payload under Bazel's undeclared outputs root.
func WritePayloadFile(kind PayloadKind, jsonPayload []byte) error {
	if kind != PayloadKindTests && kind != PayloadKindCoverage && kind != PayloadKindTelemetry {
		logger.Debug("civisibility: refusing to write unsupported payload file kind %q", kind)
		return fmt.Errorf("unsupported payload file kind %q", kind)
	}

	mode := CurrentMode()
	if !mode.PayloadFilesEnabled {
		logger.Debug("civisibility: payload-file mode disabled; refusing to write %s payload file", kind)
		return errors.New("payload file mode is disabled")
	}
	if mode.PayloadsRoot == "" {
		logger.Debug("civisibility: payload-file mode enabled for %s payloads but %s is empty", kind, UndeclaredOutputsDirEnv)
		return fmt.Errorf("%s is required when %s is enabled", UndeclaredOutputsDirEnv, PayloadsInFilesEnv)
	}

	outDir := filepath.Join(mode.PayloadsRoot, string(kind))
	absoluteOutDir := absolutePathForLog(outDir)
	logger.Debug("civisibility: ensuring %s payload output directory exists at %s", kind, absoluteOutDir)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		logger.Debug("civisibility: failed to create %s payload output directory %s: %v", kind, absoluteOutDir, err.Error())
		return fmt.Errorf("creating payload output dir: %w", err)
	}

	seq := atomic.AddUint64(&payloadFileCount, 1)
	fileName := payloadFileName(kind, seq)
	filePath := filepath.Join(outDir, fileName)
	absoluteFilePath := absolutePathForLog(filePath)
	logger.Debug("civisibility: writing %s payload file to %s", kind, absoluteFilePath)

	if err := os.WriteFile(filePath, jsonPayload, 0o644); err != nil {
		logger.Debug("civisibility: failed writing %s payload file to %s: %v", kind, absoluteFilePath, err.Error())
		return fmt.Errorf("writing payload file: %w", err)
	}
	logger.Debug("civisibility: wrote %s payload file to %s", kind, absoluteFilePath)
	return nil
}

// payloadFileName returns the filename to use for a payload of the given kind.
// Telemetry filenames are ordinal-first to preserve replay order; tests and coverage keep their historical shape.
func payloadFileName(kind PayloadKind, seq uint64) string {
	if kind == PayloadKindTelemetry {
		// Telemetry replay order matters, so keep filenames lexicographically ordered by emission sequence.
		return fmt.Sprintf("%s-%020d-%d.json", kind, seq, os.Getpid())
	}
	return fmt.Sprintf("%s-%d-%d-%d.json", kind, time.Now().UnixNano(), os.Getpid(), seq)
}

// resolveMode inspects the Bazel-related environment variables and builds the process-wide compatibility mode.
func resolveMode() Mode {
	mode := Mode{}

	manifestRloc := strings.TrimSpace(env.Get(ManifestFilePathEnv))
	payloadFilesEnv := strings.TrimSpace(env.Get(PayloadsInFilesEnv))
	undeclaredOutputsDir := strings.TrimSpace(env.Get(UndeclaredOutputsDirEnv))
	logger.Debug("civisibility: resolving test optimization mode [manifest_env:%q payload_files_env:%q undeclared_outputs_dir:%q]",
		manifestRloc, payloadFilesEnv, undeclaredOutputsDir)

	if manifestRloc != "" {
		logger.Debug("civisibility: resolving manifest path from %q", manifestRloc)
		if manifestPath, ok := resolveManifestPath(manifestRloc); ok {
			mode.ManifestPath = manifestPath
			mode.ManifestDir = filepath.Dir(manifestPath)
			mode.ManifestEnabled = isManifestVersionSupported(manifestPath)
			logger.Debug("civisibility: resolved manifest [path:%s enabled:%t]", TestOptimizationPathForLog(manifestPath), mode.ManifestEnabled)
		} else {
			logger.Debug("civisibility: could not resolve manifest path from %q", manifestRloc)
		}
	}

	mode.PayloadFilesEnabled = parseBoolEnv(payloadFilesEnv)
	if mode.PayloadFilesEnabled {
		if undeclaredOutputsDir != "" {
			mode.PayloadsRoot = filepath.Join(undeclaredOutputsDir, "payloads")
			logger.Debug("civisibility: payload-file mode enabled [root:%s]", absolutePathForLog(mode.PayloadsRoot))
		} else {
			logger.Debug("civisibility: payload-file mode enabled but %s is empty", UndeclaredOutputsDirEnv)
		}
	} else if payloadFilesEnv != "" {
		logger.Debug("civisibility: payload-file mode disabled after parsing value %q", payloadFilesEnv)
	}

	logger.Debug("civisibility: test optimization mode [manifest:%t payload_files:%t manifest_file:%s payload_root:%s]",
		mode.ManifestEnabled, mode.PayloadFilesEnabled, TestOptimizationPathForLog(mode.ManifestPath), absolutePathForLog(mode.PayloadsRoot))
	return mode
}

// parseBoolEnv accepts the same syntax as strconv.ParseBool while treating invalid values as disabled.
func parseBoolEnv(raw string) bool {
	parsed, err := strconv.ParseBool(raw)
	return err == nil && parsed
}

// resolveManifestPath resolves a manifest rlocation using direct, runfiles-dir, manifest-file, and test-srcdir lookups.
func resolveManifestPath(p string) (string, bool) {
	if normalized, ok := existingFilePath(p); ok {
		logger.Debug("civisibility: resolved manifest directly from %q to %s", p, TestOptimizationPathForLog(normalized))
		return normalized, true
	}

	if runfilesDir := strings.TrimSpace(env.Get("RUNFILES_DIR")); runfilesDir != "" {
		candidate := filepath.Join(runfilesDir, p)
		logger.Debug("civisibility: attempting manifest resolution via RUNFILES_DIR [dir:%s candidate:%s]", absolutePathForLog(runfilesDir), absolutePathForLog(candidate))
		if normalized, ok := existingFilePath(candidate); ok {
			logger.Debug("civisibility: resolved manifest via RUNFILES_DIR to %s", TestOptimizationPathForLog(normalized))
			return normalized, true
		}
	}

	if runfilesManifest := strings.TrimSpace(env.Get("RUNFILES_MANIFEST_FILE")); runfilesManifest != "" {
		logger.Debug("civisibility: attempting manifest resolution via RUNFILES_MANIFEST_FILE [manifest:%s rlocation:%s]",
			absolutePathForLog(runfilesManifest), p)
		if candidate, ok := resolveRunfilesManifestEntry(runfilesManifest, p); ok {
			if normalized, exists := existingFilePath(candidate); exists {
				logger.Debug("civisibility: resolved manifest via RUNFILES_MANIFEST_FILE to %s", TestOptimizationPathForLog(normalized))
				return normalized, true
			}
		}
	}

	if testSrcDir := strings.TrimSpace(env.Get("TEST_SRCDIR")); testSrcDir != "" {
		candidate := filepath.Join(testSrcDir, p)
		logger.Debug("civisibility: attempting manifest resolution via TEST_SRCDIR [dir:%s candidate:%s]", absolutePathForLog(testSrcDir), absolutePathForLog(candidate))
		if normalized, ok := existingFilePath(candidate); ok {
			logger.Debug("civisibility: resolved manifest via TEST_SRCDIR to %s", TestOptimizationPathForLog(normalized))
			return normalized, true
		}
	}

	logger.Debug("civisibility: manifest path %q could not be resolved from direct path, RUNFILES_DIR, RUNFILES_MANIFEST_FILE, or TEST_SRCDIR", p)
	return "", false
}

// existingFilePath returns an absolute path for an existing file, or false when the path does not currently resolve.
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

// resolveRunfilesManifestEntry scans a Bazel runfiles manifest for the requested rlocation.
func resolveRunfilesManifestEntry(manifestFilePath string, rlocation string) (string, bool) {
	logger.Debug("civisibility: reading runfiles manifest %s for rlocation %s", absolutePathForLog(manifestFilePath), rlocation)
	file, err := os.Open(manifestFilePath)
	if err != nil {
		logger.Debug("civisibility: failed to open runfiles manifest %s: %v", absolutePathForLog(manifestFilePath), err.Error())
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
			resolvedPath := strings.TrimSpace(line[splitAt+1:])
			logger.Debug("civisibility: runfiles manifest resolved rlocation %s to %s", rlocation, absolutePathForLog(resolvedPath))
			return resolvedPath, true
		}
	}
	if err := scanner.Err(); err != nil {
		logger.Debug("civisibility: failed while scanning runfiles manifest %s: %v", absolutePathForLog(manifestFilePath), err.Error())
		return "", false
	}
	logger.Debug("civisibility: runfiles manifest %s did not contain rlocation %s", absolutePathForLog(manifestFilePath), rlocation)
	return "", false
}

// isManifestVersionSupported reads the manifest version line and accepts only formats supported by this library.
func isManifestVersionSupported(manifestPath string) bool {
	logger.Debug("civisibility: reading %s", TestOptimizationPathForLog(manifestPath))
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		logger.Debug("civisibility: failed to read %s: %v", TestOptimizationPathForLog(manifestPath), err.Error())
		return false
	}

	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		rawVersionLine := strings.TrimSpace(scanner.Text())
		if rawVersionLine == "" {
			continue
		}
		version := parseManifestVersion(rawVersionLine)
		supported := version == "1"
		logger.Debug("civisibility: manifest version line %q [parsed:%q supported:%t file:%s]",
			rawVersionLine, version, supported, TestOptimizationPathForLog(manifestPath))
		return supported
	}
	if err := scanner.Err(); err != nil {
		logger.Debug("civisibility: failed while scanning %s: %v", TestOptimizationPathForLog(manifestPath), err.Error())
		return false
	}
	logger.Debug("civisibility: %s did not contain a version line", TestOptimizationPathForLog(manifestPath))
	return false
}

// parseManifestVersion extracts the manifest version value from either a raw number or a version=<n> assignment.
func parseManifestVersion(rawLine string) string {
	line := strings.TrimSpace(rawLine)
	if line == "" {
		return ""
	}

	name, value, ok := strings.Cut(line, "=")
	if !ok {
		return line
	}
	if strings.TrimSpace(name) != "version" {
		return line
	}
	return strings.TrimSpace(value)
}

// absolutePathForLog best-effort normalizes a path to an absolute form for debug logging.
func absolutePathForLog(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

// TestOptimizationPathForLog shortens runfiles cache paths to .testoptimization-relative form when available.
func TestOptimizationPathForLog(path string) string {
	if path == "" {
		return ""
	}

	normalized := filepath.ToSlash(absolutePathForLog(path))
	if normalized == ".testoptimization" || strings.HasPrefix(normalized, ".testoptimization/") {
		return normalized
	}
	if strings.HasSuffix(normalized, "/.testoptimization") {
		return ".testoptimization"
	}
	if idx := strings.Index(normalized, "/.testoptimization/"); idx >= 0 {
		return normalized[idx+1:]
	}
	return absolutePathForLog(path)
}

// ResetForTesting resets cached mode state.
// This helper is intended for tests that set per-test environment combinations.
func ResetForTesting() {
	modeMu.Lock()
	defer modeMu.Unlock()
	modeOnce = sync.Once{}
	currentMode = Mode{}
	atomic.StoreUint64(&payloadFileCount, 0)
}
