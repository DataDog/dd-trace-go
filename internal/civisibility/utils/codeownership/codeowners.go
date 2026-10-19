// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package codeownership parses GitHub and GitLab CODEOWNERS and resolves
// immutable ownership for repository-relative files and package directories.
// It has no dependency on CI initialization or process-global configuration.
//
// The rules and file discovery follow dd-trace-dotnet's implementation:
// https://github.com/DataDog/dd-trace-dotnet/tree/843640c32bae5fe6dcdf790906f6431fe15f6973/tracer/src/Datadog.Trace/Ci/CodeOwnership
package codeownership

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"sync/atomic"
)

// Dialect selects the repository host's CODEOWNERS rules.
type Dialect uint8

const (
	GitHub Dialect = iota
	GitLab
)

// GitHubMaximumFileSize is GitHub's three MiB CODEOWNERS limit.
// GitLab does not apply this limit.
const GitHubMaximumFileSize = 3 * 1024 * 1024

// Ownership is immutable. Callers can obtain a defensive owner list, the first
// owner for a service identity, or the test.codeowners value.
type Ownership struct {
	owners []string
	// tag is the encoded owner list, built on first use: a file holds many
	// rules, and only those that match a test's file need it.
	tag atomic.Pointer[string]
}

func newOwnership(owners []string) *Ownership {
	return &Ownership{owners: owners}
}

// Owners returns a copy; modifying it cannot change subsequent matches.
func (o *Ownership) Owners() []string {
	if o == nil {
		return nil
	}
	return slices.Clone(o.owners)
}

// FirstOwner preserves declaration order, including GitLab section order.
func (o *Ownership) FirstOwner() string {
	if o == nil || len(o.owners) == 0 {
		return ""
	}
	return o.owners[0]
}

// Tag returns the JSON owner array used by test.codeowners, or an empty string.
func (o *Ownership) Tag() string {
	if o == nil || len(o.owners) == 0 {
		return ""
	}
	if tag := o.tag.Load(); tag != nil {
		return *tag
	}
	data, _ := json.Marshal(o.owners)
	tag := string(data)
	// Concurrent first readers encode the same value; one is kept.
	o.tag.CompareAndSwap(nil, &tag)
	return *o.tag.Load()
}

// CodeOwners holds compiled rules. Matching is safe for concurrent readers.
type CodeOwners struct {
	dialect     Dialect
	rules       []rule
	sections    []section
	diagnostics int
}
type rule struct {
	glob          globPattern
	directoryGlob globPattern
	ownership     *Ownership
	exclusion     bool
	key           string // GitLab-only; released after duplicate compaction.
}
type section struct {
	rules         []rule
	hasExclusions bool
}

// Diagnostics reports malformed rules and headers. An ignored
// oversized GitHub file has no parsing diagnostics.
func (c *CodeOwners) Diagnostics() int {
	if c == nil {
		return 0
	}
	return c.diagnostics
}

// Load reads one selected file. Invalid lines are diagnosed and skipped;
// I/O failures return an error. An oversized GitHub file yields empty rules.
func Load(filename string, dialect Dialect) (*CodeOwners, error) {
	if filename == "" {
		return nil, errors.New("CODEOWNERS path is empty")
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if dialect == GitHub {
		info, err := file.Stat()
		if err != nil {
			return nil, err
		}
		if info.Size() > GitHubMaximumFileSize {
			return &CodeOwners{dialect: dialect}, nil
		}
	}
	return parseFile(file, dialect)
}

// Parse compiles UTF-8 rules separated by LF.
// File encoding, BOMs and file-size limits belong to Load. Lines have no 64 KiB limit.
// A read failure never returns a partially parsed ruleset.
func Parse(reader io.Reader, dialect Dialect) (*CodeOwners, error) {
	if dialect != GitHub && dialect != GitLab {
		return nil, errors.New("unknown CODEOWNERS dialect")
	}
	c := &CodeOwners{dialect: dialect}
	p := gitLabParser{sections: []section{{}}, named: make(map[string]int)}
	r := bufio.NewReader(reader)
	for {
		line, err := r.ReadString('\n')
		raw := strings.TrimSpace(line)
		if raw != "" {
			if dialect == GitHub {
				c.parseGitHub(raw)
			} else {
				p.parseLine(raw, &c.diagnostics)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if dialect == GitHub {
		slices.Reverse(c.rules)
	} else {
		c.sections = p.finish()
	}
	return c, nil
}

// Match finds ownership of a file relative to the repository root.
// Leading slashes are optional; backslashes are accepted as path separators.
// A matching ownerless GitHub rule returns true and an empty Ownership.
func (c *CodeOwners) Match(filename string) (*Ownership, bool) {
	return c.match(normalizePath(filename), false)
}

// MatchDirectory resolves a package directory without choosing a synthetic
// filename. Directory rules include the directory itself. Terminal /* selects
// immediate children only; other directory patterns can be inherited.
func (c *CodeOwners) MatchDirectory(directory string) (*Ownership, bool) {
	if directory == "." {
		directory = "/"
	}
	return c.match(normalizePath(directory), true)
}

func (c *CodeOwners) match(value string, directory bool) (*Ownership, bool) {
	if c == nil || value == "" {
		return nil, false
	}
	if c.dialect == GitHub {
		for i := range c.rules {
			if c.rules[i].matches(value, directory) {
				return c.rules[i].ownership, true
			}
		}
		return nil, false
	}
	var selected *Ownership
	var combined []string
	var seen map[string]bool
	for _, section := range c.sections {
		owners := section.match(value, directory)
		if owners == nil {
			continue
		}
		if selected == nil {
			selected = owners
			continue
		}
		for _, owner := range owners.owners {
			current := selected.owners
			if combined != nil {
				current = combined
			}
			if seen == nil && len(current) >= 16 {
				seen = make(map[string]bool, len(current))
				for _, value := range current {
					seen[value] = true
				}
			}
			present := false
			if seen != nil {
				present = seen[owner]
			} else {
				present = slices.Contains(current, owner)
			}
			if present {
				continue
			}
			if combined == nil {
				combined = make([]string, len(current), len(current)+len(owners.owners))
				copy(combined, current)
			}
			combined = append(combined, owner)
			// Small unions avoid a map. Large lists retain bounded lookup work, as
			// their accumulated owners are indexed once rather than rescanned.
			if seen != nil {
				seen[owner] = true
			} else if len(combined) >= 16 {
				seen = make(map[string]bool, len(combined))
				for _, value := range combined {
					seen[value] = true
				}
			}
		}
	}
	if combined != nil {
		return newOwnership(combined), true
	}
	return selected, selected != nil
}
func (r *rule) matches(value string, directory bool) bool {
	if directory {
		return r.directoryGlob.match(value)
	}
	return r.glob.match(value)
}
func (s section) match(value string, directory bool) *Ownership {
	var selected *Ownership
	for i := range s.rules {
		r := &s.rules[i]
		if !r.matches(value, directory) {
			continue
		}
		if r.exclusion {
			return nil
		}
		if selected == nil {
			selected = r.ownership
			// With no exclusions, the first matching rule in reversed order wins.
			// An ownerless winner also stops this section, exactly as the full scan.
			if !s.hasExclusions {
				break
			}
		}
	}
	if selected == nil || len(selected.owners) == 0 {
		return nil
	}
	return selected
}
func normalizePath(value string) string {
	if value == "" {
		return "/"
	}
	if strings.ContainsRune(value, '\\') {
		value = strings.ReplaceAll(value, "\\", "/")
	}
	separators := 0
	for separators < len(value) && value[separators] == '/' {
		separators++
	}
	switch {
	case separators == 0:
		return "/" + value
	case separators > 1:
		return value[separators-1:]
	default:
		return value
	}
}

func splitRule(raw string) (string, string) {
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\\' && i+1 < len(raw) {
			i++
			continue
		}
		if raw[i] == ' ' || raw[i] == '\t' {
			return raw[:i], strings.TrimSpace(raw[i:])
		}
	}
	return raw, ""
}
func uniqueOwners(owners []string) []string {
	if len(owners) < 2 {
		return owners
	}
	seen := make(map[string]bool, len(owners))
	result := make([]string, 0, len(owners))
	for _, o := range owners {
		if !seen[o] {
			seen[o] = true
			result = append(result, o)
		}
	}
	return result
}
func compileRule(pattern string, dialect Dialect, owners []string, exclusion bool) (rule, bool) {
	file, directory, ok := compileGlobs(pattern, dialect)
	if !ok {
		return rule{}, false
	}
	return rule{glob: file, directoryGlob: directory, ownership: newOwnership(owners), exclusion: exclusion}, true
}

// Owner tokens are separated by ASCII spaces or tabs.
func ownerSeparator(r rune) bool { return r == ' ' || r == '\t' }
