// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package codeowners parses CODEOWNERS files and matches repository paths to
// their owners.
package codeowners

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strings"
)

// CodeOwners holds the parsed sections of a CODEOWNERS file.
type CodeOwners struct {
	Sections []*Section
}

// Section holds entries that belong to one CODEOWNERS section.
type Section struct {
	Name    string
	Entries []Entry
}

// Entry is one CODEOWNERS pattern and its owners.
type Entry struct {
	Pattern string
	Owners  []string
	Section string
}

// New parses the CODEOWNERS file at filePath.
func New(filePath string) (*CodeOwners, error) {
	if filePath == "" {
		return nil, errors.New("filePath cannot be empty")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return Parse(file)
}

// Parse reads a CODEOWNERS file from r.
func Parse(r io.Reader) (*CodeOwners, error) {
	var entries []Entry
	var sectionNames []string
	var currentSection string

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) == 0 || line[0] == '#' {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = line[1 : len(line)-1]
			if existing := findSectionIgnoreCase(sectionNames, currentSection); existing == "" {
				sectionNames = append(sectionNames, currentSection)
			} else {
				currentSection = existing
			}
			continue
		}

		pattern := line
		var owners []string
		for term := range strings.FieldsSeq(line) {
			if len(term) == 0 {
				continue
			}
			if term[0] == '@' || strings.Contains(term, "@") {
				owners = append(owners, term)
				if pos := strings.Index(pattern, term); pos > 0 {
					pattern = pattern[:pos] + pattern[pos+len(term):]
				}
			}
		}

		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		entries = append(entries, Entry{Pattern: pattern, Owners: owners, Section: currentSection})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Match checks entries in reverse file order so the last matching entry
	// has precedence.
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	result := &CodeOwners{}
	for _, entry := range entries {
		var section *Section
		for _, candidate := range result.Sections {
			if candidate.Name == entry.Section {
				section = candidate
				break
			}
		}
		if section == nil {
			section = &Section{Name: entry.Section, Entries: []Entry{}}
			result.Sections = append(result.Sections, section)
		}
		section.Entries = append(section.Entries, entry)
	}
	return result, nil
}

func findSectionIgnoreCase(sections []string, section string) string {
	sectionLower := strings.ToLower(section)
	for _, candidate := range sections {
		if strings.ToLower(candidate) == sectionLower {
			return candidate
		}
	}
	return ""
}

// GetSection returns the first section with the specified name.
func (co *CodeOwners) GetSection(section string) *Section {
	for _, candidate := range co.Sections {
		if candidate.Name == section {
			return candidate
		}
	}
	return nil
}

// Match returns the first matching entry from each section. If entries from
// multiple sections match, Match combines them into one entry.
func (co *CodeOwners) Match(value string) (*Entry, bool) {
	if co == nil {
		return nil, false
	}
	var matches []Entry

	for _, section := range co.Sections {
		for _, entry := range section.Entries {
			pattern := entry.Pattern
			finalPattern := pattern

			var includeAnythingBefore, includeAnythingAfter bool
			if strings.HasPrefix(pattern, "/") {
				includeAnythingBefore = false
			} else {
				if strings.HasPrefix(finalPattern, "*") {
					finalPattern = finalPattern[1:]
				}
				includeAnythingBefore = true
			}

			if strings.HasSuffix(pattern, "/") {
				includeAnythingAfter = true
			} else if strings.HasSuffix(pattern, "/*") {
				includeAnythingAfter = true
				finalPattern = finalPattern[:len(finalPattern)-1]
			}

			if includeAnythingAfter {
				found := includeAnythingBefore && strings.Contains(value, finalPattern) || strings.HasPrefix(value, finalPattern)
				if !found {
					continue
				}
				if !strings.HasSuffix(pattern, "/*") {
					matches = append(matches, entry)
					break
				}
				patternEnd := strings.Index(value, finalPattern)
				if patternEnd != -1 {
					remaining := value[patternEnd+len(finalPattern):]
					if !strings.Contains(remaining, "/") {
						matches = append(matches, entry)
						break
					}
				}
			} else if includeAnythingBefore {
				if strings.HasSuffix(value, finalPattern) {
					matches = append(matches, entry)
					break
				}
			} else if value == finalPattern {
				matches = append(matches, entry)
				break
			}
		}
	}

	switch len(matches) {
	case 0:
		return nil, false
	case 1:
		return &matches[0], true
	default:
		patterns := make([]string, 0, len(matches))
		owners := make([]string, 0)
		sections := make([]string, 0, len(matches))
		for _, entry := range matches {
			patterns = append(patterns, entry.Pattern)
			owners = append(owners, entry.Owners...)
			sections = append(sections, entry.Section)
		}
		return &Entry{
			Pattern: strings.Join(patterns, " | "),
			Owners:  owners,
			Section: strings.Join(sections, " | "),
		}, true
	}
}

// GetOwnersString returns the owners as a JSON-compatible string list.
func (e *Entry) GetOwnersString() string {
	if len(e.Owners) == 0 {
		return ""
	}
	return "[\"" + strings.Join(e.Owners, "\",\"") + "\"]"
}
