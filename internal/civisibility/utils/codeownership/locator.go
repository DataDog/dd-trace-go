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

// Locations supplies explicit repository context used to select the dialect.
// No environment is read.
type Locations struct{ Repository, Provider string }

// Resolver owns a selected file and matches paths relative to the directory
// given to DiscoverIn. Its decision is stable for a test session.
type Resolver struct {
	rules    *CodeOwners
	filename string
	dialect  Dialect
}

// File is the selected CODEOWNERS path.
func (r *Resolver) File() string { return r.filename }

// Dialect reports which host's rules were selected.
func (r *Resolver) Dialect() Dialect { return r.dialect }

// Diagnostics reports invalid input without exposing mutable parsed state.
func (r *Resolver) Diagnostics() int { return r.rules.Diagnostics() }

// Ignored reports whether the selected file was ignored for its size.
func (r *Resolver) Ignored() bool { return r.rules.Ignored() }

// Match resolves a file path relative to the directory given to DiscoverIn.
func (r *Resolver) Match(value string) (*Ownership, bool) {
	if r == nil {
		return nil, false
	}
	value = normalizePath(value)
	// Reject traversal before Clean can hide it: such a path leaves the
	// directory whose CODEOWNERS file was selected.
	for segment := range strings.SplitSeq(value, "/") {
		if segment == ".." {
			return nil, false
		}
	}
	return r.rules.Match(path.Clean(value))
}

// DiscoverIn selects the host's first existing CODEOWNERS location in
// directory itself, without looking for a Git root, and matches paths relative
// to directory. An unreadable selected file returns an error; it never
// silently switches ownership to another file.
func DiscoverIn(directory string, locations Locations) (*Resolver, error) {
	directory, err := absoluteDirectory(directory)
	if err != nil || directory == "" {
		return nil, err
	}
	dialect, err := detectDialect(directory, locations.Repository, locations.Provider)
	if err != nil {
		return nil, err
	}
	folders := []string{".github", "", "docs"}
	if dialect == GitLab {
		folders = []string{"", "docs", ".gitlab"}
	}
	for _, folder := range folders {
		filename := filepath.Join(directory, folder, "CODEOWNERS")
		ok, err := candidate(directory, filename)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		rules, err := Load(filename, dialect)
		if err != nil {
			return nil, err
		}
		return &Resolver{rules: rules, filename: filename, dialect: dialect}, nil
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

// detectDialect picks the host from the repository URL, then the CI provider,
// then a lone .gitlab/CODEOWNERS. An error reading those markers is returned,
// so discovery is retried instead of settling on the wrong host.
func detectDialect(root, repository, provider string) (Dialect, error) {
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
		return GitLab, nil
	}
	if host == "github.com" {
		return GitHub, nil
	}
	if provider == "gitlab" {
		return GitLab, nil
	}
	if provider == "github" {
		return GitHub, nil
	}
	// Only files discovery would read count, so a skipped file cannot select a
	// dialect whose locations then hold nothing to read.
	gitLab, err := candidate(root, filepath.Join(root, ".gitlab", "CODEOWNERS"))
	if err != nil {
		return GitHub, err
	}
	gitHub, err := candidate(root, filepath.Join(root, ".github", "CODEOWNERS"))
	if err != nil {
		return GitHub, err
	}
	if gitLab && !gitHub {
		return GitLab, nil
	}
	return GitHub, nil
}

// candidate reports whether discovery may read filename: a regular file that
// resolves inside directory. A device or named pipe could block or never end,
// and a committed symlink can point anywhere, including files such as
// /proc/kmsg that look regular but block. Both paths are resolved, so a
// directory reached through a symlink, as macOS's /var is, or a Windows short
// name still contains its files. Missing and skipped files are not errors;
// Load checks the type again.
func candidate(directory, filename string) (bool, error) {
	info, err := os.Stat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}
	resolved, err := filepath.EvalSymlinks(filename)
	if err != nil {
		return false, err
	}
	root, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return false, err
	}
	return inside(root, resolved), nil
}
func inside(directory, filename string) bool {
	relative, err := filepath.Rel(directory, filename)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
