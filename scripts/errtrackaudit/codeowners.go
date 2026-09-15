// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// codeownersFile is the repository-relative location of the CODEOWNERS file.
const codeownersFile = "CODEOWNERS"

// unownedPlaceholder groups call sites whose file matches no CODEOWNERS entry.
// make lint/misc (scripts/check_codeowners.go) enforces that every tracked
// file is owned, so in practice this should only appear on untracked files.
const unownedPlaceholder = "(unowned)"

// The repository's CODEOWNERS is restricted by scripts/check_codeowners.go to
// the subset of patterns GitHub and CI Visibility interpret identically (see
// "CODEOWNERS patterns" in CONTRIBUTING.md), so this parser accepts exactly
// that subset:
//
//	/path/to/dir/   anchored, applies to everything beneath the directory
//	/path/to/file   anchored, exact match
//	*suffix         suffix match at any depth, one leading "*", no "/"
//
// As on GitHub, a later matching entry takes precedence over an earlier one.
type ownersKind int

const (
	ownersDir ownersKind = iota
	ownersFile
	ownersSuffix
)

type ownersRule struct {
	kind   ownersKind
	match  string // pattern with the leading "/" or "*" removed
	owners []string
}

type codeowners struct {
	rules []ownersRule
}

func loadCodeowners(path string) (*codeowners, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	defer f.Close()
	var co codeowners
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Fields(text)
		if len(fields) == 1 {
			return nil, fmt.Errorf("%s:%d: pattern %q has no owners", path, line, fields[0])
		}
		r, err := parseOwnersPattern(fields[0])
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		r.owners = fields[1:]
		co.rules = append(co.rules, r)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return &co, nil
}

// unsupportedWildcards are gitignore metacharacters, beyond a single leading
// suffix "*", that GitHub interprets but the CI Visibility matcher treats as
// literal characters.
const unsupportedWildcards = "*?[]\\"

// parseOwnersPattern validates one CODEOWNERS pattern and reduces it to its
// comparison form, mirroring the validation in scripts/check_codeowners.go so
// the two cannot drift: the "*" and "/" catch-alls are rejected, wildcards
// are rejected in anchored paths, and a suffix "*" pattern must be a bare
// suffix. "@" is rejected because the CI Visibility parser reads any pattern
// token containing it as an owner rather than part of the path.
func parseOwnersPattern(pattern string) (ownersRule, error) {
	if strings.Contains(pattern, "@") {
		return ownersRule{}, errors.New(`"@" in a path is parsed as an owner by CI Visibility rather than the pattern; rename the path or use a directory-level rule instead`)
	}
	switch {
	case strings.HasPrefix(pattern, "*"):
		suffix := pattern[1:]
		switch {
		case suffix == "":
			return ownersRule{}, errors.New(`the "*" catch-all is not allowed; every path must have an explicit owner`)
		case strings.Contains(suffix, "/"):
			return ownersRule{}, errors.New(`a "*" pattern must be a bare suffix containing no "/", e.g. "*appsec.go"`)
		case strings.ContainsAny(suffix, unsupportedWildcards):
			return ownersRule{}, errors.New(`"*", "?", "[", "]", and "\\" are gitignore wildcards that CI Visibility treats as literal characters; use a plain suffix`)
		}
		return ownersRule{kind: ownersSuffix, match: suffix}, nil
	case strings.HasPrefix(pattern, "/"):
		if strings.ContainsAny(pattern, unsupportedWildcards) {
			return ownersRule{}, errors.New(`wildcards are not supported in anchored paths; use a "/dir/" or "*suffix" pattern instead`)
		}
		match := pattern[1:]
		if strings.HasSuffix(pattern, "/") {
			if match == "" {
				return ownersRule{}, errors.New(`the "/" catch-all is not allowed; every path must have an explicit owner`)
			}
			return ownersRule{kind: ownersDir, match: match}, nil
		}
		return ownersRule{kind: ownersFile, match: match}, nil
	}
	return ownersRule{}, errors.New(`pattern must be anchored with a leading "/" or be a "*suffix" pattern`)
}

// matches reports whether the rule applies to the given repository-relative
// path (slash-separated, no leading "/").
func (r ownersRule) matches(path string) bool {
	switch r.kind {
	case ownersDir:
		// r.match keeps the trailing "/" ("internal/" from "/internal/"),
		// which is what makes the match recursive.
		return strings.HasPrefix(path, r.match)
	case ownersFile:
		return path == r.match
	case ownersSuffix:
		if strings.HasSuffix(path, r.match) {
			return true
		}
		// gitignore matches an unanchored pattern against every path
		// component, and matching a directory implies matching its entire
		// subtree. A suffix pattern can therefore match a directory name
		// partway through the path, not just the final filename: "*appsec.go"
		// must own "special/appsec.go/child.go" the same way GitHub does.
		for i := 0; i < len(path); i++ {
			if path[i] == '/' && strings.HasSuffix(path[:i], r.match) {
				return true
			}
		}
		return false
	}
	return false
}

// ownersFor returns the owners of a repository-relative path: the owners of the
// last matching entry, mirroring GitHub's "last match wins" precedence.
func (c *codeowners) ownersFor(repoRelPath string) []string {
	var matched []string
	for _, r := range c.rules {
		if r.matches(repoRelPath) {
			matched = r.owners
		}
	}
	if matched == nil {
		return []string{unownedPlaceholder}
	}
	return matched
}
