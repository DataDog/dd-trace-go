// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
)

// maxListedOwners is the largest number of owners the board shows per cell.
const maxListedOwners = 3

// ownerRule is one CODEOWNERS line.
type ownerRule struct {
	match   *regexp.Regexp
	owners  []string // handles without the leading "@"
	negated bool
}

// codeowners resolves the owners of file paths.
//
// The matcher translates each pattern to a regular expression. It handles
// anchoring with a leading or inner "/", directory patterns with a trailing
// "/", "*", "?" and "**". It does not implement full gitignore semantics:
// character classes, backslash escapes, and "**" that matches zero directories
// in the middle of a pattern are not supported. A "!" prefix removes the owners
// of the files that match, a behavior of this tool only, because GitHub
// ignores negated patterns. The repository restricts CODEOWNERS to patterns
// that GitHub and this matcher read in the same way (see CONTRIBUTING.md).
//
// The repository has two more CODEOWNERS consumers:
// internal/civisibility/utils/codeowners.go and scripts/check_codeowners.go.
// GitHub and CI Visibility read the file differently (see CONTRIBUTING.md).
type codeowners struct {
	rules []ownerRule
}

// parseCodeowners parses the text of a CODEOWNERS file. A nil result is valid
// and owns nothing.
func parseCodeowners(text string) *codeowners {
	co := &codeowners{}
	for line := range strings.SplitSeq(text, "\n") {
		if before, _, found := strings.Cut(line, " #"); found {
			line = before
		}
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		pattern := fields[0]
		negated := strings.HasPrefix(pattern, "!")
		pattern = strings.TrimPrefix(pattern, "!")
		if pattern == "" {
			continue
		}
		var owners []string
		for _, f := range fields[1:] {
			if handle, ok := strings.CutPrefix(f, "@"); ok && handle != "" {
				owners = append(owners, handle)
			}
		}
		co.rules = append(co.rules, ownerRule{match: patternRegexp(pattern), owners: owners, negated: negated})
	}
	return co
}

// patternRegexp translates a CODEOWNERS pattern to a regular expression over
// slash-separated paths. The expression matches the named path and everything
// below it.
func patternRegexp(pattern string) *regexp.Regexp {
	dirOnly := strings.HasSuffix(pattern, "/")
	p := strings.TrimSuffix(pattern, "/")
	anchored := strings.HasPrefix(p, "/")
	p = strings.TrimPrefix(p, "/")
	if rest, ok := strings.CutPrefix(p, "**/"); ok {
		p, anchored = rest, false
	} else if strings.Contains(p, "/") {
		anchored = true
	}

	var b strings.Builder
	b.WriteString("^")
	if !anchored {
		b.WriteString("(?:.*/)?")
	}
	for p != "" {
		i := strings.IndexAny(p, "*?")
		if i < 0 {
			b.WriteString(regexp.QuoteMeta(p))
			break
		}
		b.WriteString(regexp.QuoteMeta(p[:i]))
		switch {
		case strings.HasPrefix(p[i:], "**"):
			b.WriteString(".*")
			p = p[i+2:]
		case p[i] == '*':
			b.WriteString("[^/]*")
			p = p[i+1:]
		default:
			b.WriteString("[^/]")
			p = p[i+1:]
		}
	}
	if dirOnly {
		b.WriteString("/.*$")
	} else {
		b.WriteString("(?:/.*)?$")
	}
	return regexp.MustCompile(b.String())
}

// ownersOf returns the owners of file. The last matching rule wins.
func (c *codeowners) ownersOf(file string) []string {
	if c == nil {
		return nil
	}
	var owners []string
	for _, r := range c.rules {
		if !r.match.MatchString(file) {
			continue
		}
		if r.negated {
			owners = nil
		} else {
			owners = r.owners
		}
	}
	return owners
}

// ownersOfFiles returns the owners of all files, without exclude (the PR
// author). Owners that cover more files come first, then owners in order of
// first appearance.
func (c *codeowners) ownersOfFiles(files []string, exclude string) []string {
	type tally struct {
		handle string
		files  int
	}
	var tallies []*tally
	index := make(map[string]*tally)
	for _, f := range files {
		for _, handle := range c.ownersOf(f) {
			key := strings.ToLower(handle)
			if key == strings.ToLower(exclude) {
				continue
			}
			t, ok := index[key]
			if !ok {
				t = &tally{handle: handle}
				index[key] = t
				tallies = append(tallies, t)
			}
			t.files++
		}
	}
	slices.SortStableFunc(tallies, func(a, b *tally) int { return cmp.Compare(b.files, a.files) })
	owners := make([]string, len(tallies))
	for i, t := range tallies {
		owners[i] = t.handle
	}
	return owners
}

// proposedOwners returns owners when none of them is in current. Otherwise the
// pull request already has an owner and nothing is proposed.
func proposedOwners(current, owners []string) []string {
	for _, o := range owners {
		if slices.ContainsFunc(current, func(c string) bool { return strings.EqualFold(c, o) }) {
			return nil
		}
	}
	return owners
}
