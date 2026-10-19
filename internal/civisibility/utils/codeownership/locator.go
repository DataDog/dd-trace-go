// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package codeownership

import (
	"errors"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Locations supplies explicit repository context. No environment is read.
// SourceRoot is a directory; Workspace is the base of paths used by callers.
type Locations struct{ Workspace, SourceRoot, Repository, Provider string }

// Resolver owns a selected file and rebases workspace paths when the workspace
// is a subdirectory of the Git root. Its decision is stable for a test session.
type Resolver struct {
	rules          *CodeOwners
	root, filename string
	dialect        Dialect
	prefix         string
}

// Root is the repository directory used to locate CODEOWNERS.
func (r *Resolver) Root() string { return r.root }

// File is the selected CODEOWNERS path.
func (r *Resolver) File() string { return r.filename }

// Dialect reports which host's rules were selected.
func (r *Resolver) Dialect() Dialect { return r.dialect }

// Diagnostics reports invalid input without exposing mutable parsed state.
func (r *Resolver) Diagnostics() int { return r.rules.Diagnostics() }

// Match resolves a logical file path relative to the supplied workspace.
func (r *Resolver) Match(value string) (*Ownership, bool) {
	if r == nil {
		return nil, false
	}
	value, ok := r.repositoryPath(value)
	if !ok {
		return nil, false
	}
	return r.rules.Match(value)
}

// MatchDirectory resolves a logical package directory relative to the workspace.
func (r *Resolver) MatchDirectory(value string) (*Ownership, bool) {
	if r == nil {
		return nil, false
	}
	value, ok := r.repositoryPath(value)
	if !ok {
		return nil, false
	}
	return r.rules.MatchDirectory(value)
}
func (r *Resolver) repositoryPath(value string) (string, bool) {
	value = normalizePath(value)
	if value == "" {
		return "", false
	}
	// Compiler paths are resolved by the caller. Reject traversal before Clean
	// can hide it, including attempts to leave the workspace but stay in the repo.
	for segment := range strings.SplitSeq(value, "/") {
		if segment == ".." {
			return "", false
		}
	}
	if r.prefix != "" {
		value = "/" + r.prefix + value
	}
	return path.Clean(value), true
}

// Discover finds the nearest Git root (directory or worktree .git file), then
// selects the host's first existing CODEOWNERS location. An unreadable selected
// file returns an error; it never silently switches ownership to another file.
func Discover(locations Locations) (*Resolver, error) {
	workspace, err := absoluteDirectory(locations.Workspace)
	if err != nil {
		return nil, err
	}
	source, err := absoluteDirectory(locations.SourceRoot)
	if err != nil {
		return nil, err
	}
	root := findGitRoot(source)
	if root == "" {
		root = findGitRoot(workspace)
	}
	if root != "" {
		return discoverAt(root, workspace, locations)
	}
	// A repository-less standalone binary can still use its explicit workspace.
	if workspace != "" {
		result, err := discoverAt(workspace, workspace, locations)
		if err != nil || result != nil {
			return result, err
		}
	}
	if source != "" && source != workspace {
		return discoverAt(source, source, locations)
	}
	return nil, nil
}

// DiscoverIn selects the host's first CODEOWNERS location in directory itself,
// without looking for a Git root, and matches paths relative to directory.
func DiscoverIn(directory string, locations Locations) (*Resolver, error) {
	directory, err := absoluteDirectory(directory)
	if err != nil || directory == "" {
		return nil, err
	}
	return discoverAt(directory, directory, locations)
}

func discoverAt(root, workspace string, locations Locations) (*Resolver, error) {
	dialect := detectDialect(root, locations.Repository, locations.Provider)
	folders := []string{".github", "", "docs"}
	if dialect == GitLab {
		folders = []string{"", "docs", ".gitlab"}
	}
	for _, folder := range folders {
		filename := filepath.Join(root, folder, "CODEOWNERS")
		info, err := os.Stat(filename)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			continue
		}
		rules, err := Load(filename, dialect)
		if err != nil {
			return nil, err
		}
		if workspace == "" {
			workspace = root
		}
		relative, err := filepath.Rel(root, workspace)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, nil
		}
		prefix := filepath.ToSlash(relative)
		if prefix == "." {
			prefix = ""
		}
		return &Resolver{rules: rules, root: root, filename: filename, dialect: dialect, prefix: prefix}, nil
	}
	return nil, nil
}
func absoluteDirectory(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	value, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	if canonical, err := filepath.EvalSymlinks(value); err == nil {
		value = canonical
	}
	return value, nil
}
func findGitRoot(start string) string {
	for start != "" {
		if info, err := os.Stat(filepath.Join(start, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return start
		}
		parent := filepath.Dir(start)
		if parent == start {
			break
		}
		start = parent
	}
	return ""
}
func detectDialect(root, repository, provider string) Dialect {
	host := ""
	if u, err := url.Parse(repository); err == nil {
		host = u.Hostname()
	}
	if host == "" {
		if at := strings.IndexByte(repository, '@'); at >= 0 {
			if colon := strings.IndexByte(repository[at+1:], ':'); colon >= 0 {
				host = repository[at+1 : at+1+colon]
			}
		}
	}
	host = strings.ToLower(host)
	if host == "gitlab.com" || strings.HasPrefix(host, "gitlab.") || strings.Contains(host, ".gitlab.") {
		return GitLab
	}
	if host == "github.com" {
		return GitHub
	}
	if provider == "gitlab" {
		return GitLab
	}
	if provider == "github" {
		return GitHub
	}
	if regularFile(filepath.Join(root, ".gitlab", "CODEOWNERS")) && !regularFile(filepath.Join(root, ".github", "CODEOWNERS")) {
		return GitLab
	}
	return GitHub
}
func regularFile(filename string) bool {
	info, err := os.Stat(filename)
	return err == nil && !info.IsDir()
}
