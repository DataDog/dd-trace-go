// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package utils

import (
	"net"
	"net/url"
	"path"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
)

// SourceFilePath contains the source path variants CI Visibility needs for one runtime source file.
type SourceFilePath struct {
	// RuntimePath is the raw path returned by runtime.Func.FileLine.
	RuntimePath string
	// FilesystemPath is the best-effort local filesystem path used for source parsing.
	FilesystemPath string
	// RelativePath is the slash-normalized repository path used for tags, CODEOWNERS, and impacted tests.
	RelativePath string
	// FilesystemKnown is true when FilesystemPath is derived from a trusted filesystem or workspace path.
	FilesystemKnown bool
}

// ResolveSourceFilePathFromCITags resolves a runtime source path into tag and filesystem forms.
func ResolveSourceFilePathFromCITags(sourcePath string) SourceFilePath {
	return resolveSourceFilePath(sourcePath, GetCITags(), buildInfoMainModulePath())
}

// resolveSourceFilePath resolves a runtime source path with injectable inputs for deterministic tests.
func resolveSourceFilePath(sourcePath string, tags map[string]string, mainModulePath string) SourceFilePath {
	if sourcePath == "" {
		return SourceFilePath{}
	}

	result := SourceFilePath{
		RuntimePath:     sourcePath,
		FilesystemPath:  sourcePath,
		RelativePath:    sourcePath,
		FilesystemKnown: false,
	}

	workspacePath := tags[constants.CIWorkspacePath]
	if filepath.IsAbs(sourcePath) {
		cleanedPath := filepath.Clean(sourcePath)
		result.FilesystemPath = cleanedPath
		result.FilesystemKnown = true
		result.RelativePath = filepath.ToSlash(cleanedPath)
		if relPath, ok := relativePathInsideWorkspace(workspacePath, cleanedPath); ok {
			result.RelativePath = filepath.ToSlash(relPath)
		}
		return result
	}

	logicalPath := cleanLogicalSourcePath(sourcePath)
	result.RelativePath = logicalPath

	repoPrefix := repositoryPathFromURL(tags[constants.GitRepositoryURL])
	if relativePath, ok := trimLogicalPrefix(logicalPath, repoPrefix); ok {
		relativePath = stripConfirmedSemanticImportVersion(relativePath, repoPrefix, mainModulePath)
		result.RelativePath = relativePath
		if workspacePath != "" {
			result.FilesystemPath = filepath.Join(filepath.Clean(workspacePath), filepath.FromSlash(relativePath))
			result.FilesystemKnown = true
		}
		return result
	}

	if relativePath, ok := trimLogicalPrefix(logicalPath, mainModulePath); ok && mainModulePath != "" && mainModulePath != "command-line-arguments" {
		result.RelativePath = relativePath
		return result
	}

	if isWorkspaceRelativeLogicalPath(logicalPath) {
		result.RelativePath = logicalPath
		if workspacePath != "" {
			result.FilesystemPath = filepath.Join(filepath.Clean(workspacePath), filepath.FromSlash(logicalPath))
			result.FilesystemKnown = true
		}
		return result
	}

	return result
}

// relativePathInsideWorkspace returns a clean relative path only when filePath is inside workspacePath.
func relativePathInsideWorkspace(workspacePath, filePath string) (string, bool) {
	if workspacePath == "" {
		return "", false
	}
	relPath, err := filepath.Rel(filepath.Clean(workspacePath), filepath.Clean(filePath))
	if err != nil || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) || filepath.IsAbs(relPath) {
		return "", false
	}
	return relPath, true
}

// cleanLogicalSourcePath normalizes a non-absolute runtime path for logical repository matching.
func cleanLogicalSourcePath(sourcePath string) string {
	logicalPath := strings.ReplaceAll(sourcePath, "\\", "/")
	logicalPath = path.Clean(logicalPath)
	if logicalPath == "." {
		return ""
	}
	return strings.TrimPrefix(logicalPath, "./")
}

// trimLogicalPrefix strips prefix from logicalPath only when the match ends on a path segment boundary.
func trimLogicalPrefix(logicalPath, prefix string) (string, bool) {
	if logicalPath == "" || prefix == "" {
		return "", false
	}
	prefix = strings.Trim(path.Clean(strings.ReplaceAll(prefix, "\\", "/")), "/")
	if prefix == "." || prefix == "" {
		return "", false
	}
	if logicalPath == prefix {
		return "", true
	}
	if suffix, ok := strings.CutPrefix(logicalPath, prefix+"/"); ok {
		return suffix, true
	}
	return "", false
}

// repositoryPathFromURL converts common Git remote URL forms into a Go-import-style host/path prefix.
func repositoryPathFromURL(repositoryURL string) string {
	repositoryURL = strings.TrimSpace(repositoryURL)
	if repositoryURL == "" {
		return ""
	}

	if scpHost, scpPath, ok := parseSCPStyleRepositoryURL(repositoryURL); ok {
		return cleanRepositoryHostPath(scpHost, scpPath)
	}

	parsedURL, err := url.Parse(repositoryURL)
	if err != nil || parsedURL.Host == "" {
		return ""
	}
	host := parsedURL.Hostname()
	if host == "" {
		host = parsedURL.Host
	}
	return cleanRepositoryHostPath(host, parsedURL.EscapedPath())
}

// stripConfirmedSemanticImportVersion removes a build-info-confirmed semantic import suffix from a repo-relative path.
func stripConfirmedSemanticImportVersion(relativePath, repoPrefix, mainModulePath string) string {
	moduleDir, version, ok := semanticImportVersionSuffix(mainModulePath, repoPrefix)
	if !ok {
		return relativePath
	}
	if moduleDir == "" {
		if suffix, ok := strings.CutPrefix(relativePath, version+"/"); ok {
			return suffix
		}
		return relativePath
	}
	versionedModuleDir := moduleDir + "/" + version
	if suffix, ok := trimLogicalPrefix(relativePath, versionedModuleDir); ok {
		if suffix == "" {
			return moduleDir
		}
		return moduleDir + "/" + suffix
	}
	return relativePath
}

// semanticImportVersionSuffix returns the module directory and semantic version suffix inside repoPrefix.
func semanticImportVersionSuffix(modulePath, repoPrefix string) (moduleDir string, version string, ok bool) {
	moduleRelativePath, ok := trimLogicalPrefix(modulePath, repoPrefix)
	if !ok || moduleRelativePath == "" {
		return "", "", false
	}
	lastSlash := strings.LastIndex(moduleRelativePath, "/")
	if lastSlash == -1 {
		version = moduleRelativePath
	} else {
		moduleDir = moduleRelativePath[:lastSlash]
		version = moduleRelativePath[lastSlash+1:]
	}
	if !isSemanticImportVersion(version) {
		return "", "", false
	}
	return moduleDir, version, true
}

// isSemanticImportVersion reports whether segment is a Go semantic import version suffix.
func isSemanticImportVersion(segment string) bool {
	if len(segment) < 2 || segment[0] != 'v' || segment[1] < '2' || segment[1] > '9' {
		return false
	}
	for _, r := range segment[2:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// buildInfoMainModulePath returns the main module path embedded in the current binary, if available.
func buildInfoMainModulePath() string {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return buildInfo.Main.Path
}

// parseSCPStyleRepositoryURL parses Git scp-like remotes such as git@github.com:org/repo.git.
func parseSCPStyleRepositoryURL(repositoryURL string) (host string, repositoryPath string, ok bool) {
	if strings.Contains(repositoryURL, "://") {
		return "", "", false
	}
	hostPart, repositoryPath, ok := strings.Cut(repositoryURL, ":")
	if !ok {
		return "", "", false
	}
	if atIndex := strings.LastIndex(hostPart, "@"); atIndex != -1 {
		hostPart = hostPart[atIndex+1:]
	}
	if hostPart == "" || repositoryPath == "" {
		return "", "", false
	}
	return hostPart, repositoryPath, true
}

// cleanRepositoryHostPath joins a repository host and path into a stable import-prefix candidate.
func cleanRepositoryHostPath(host, repositoryPath string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if hostWithoutPort, _, err := net.SplitHostPort(host); err == nil {
		host = hostWithoutPort
	}
	repositoryPath = strings.TrimSpace(repositoryPath)
	if unescapedPath, err := url.PathUnescape(repositoryPath); err == nil {
		repositoryPath = unescapedPath
	}
	repositoryPath = strings.Trim(path.Clean(strings.ReplaceAll(repositoryPath, "\\", "/")), "/")
	repositoryPath = strings.TrimSuffix(repositoryPath, ".git")
	if host == "" || repositoryPath == "" || repositoryPath == "." {
		return ""
	}
	return host + "/" + repositoryPath
}

// isWorkspaceRelativeLogicalPath reports whether logicalPath is safe to resolve from ci.workspace_path.
func isWorkspaceRelativeLogicalPath(logicalPath string) bool {
	if logicalPath == "" || strings.HasPrefix(logicalPath, "../") || logicalPath == ".." {
		return false
	}
	segments := strings.Split(logicalPath, "/")
	if slices.Contains(segments, "..") {
		return false
	}
	if len(segments) >= 3 && strings.ContainsAny(segments[0], ".:@") {
		return false
	}
	return true
}
