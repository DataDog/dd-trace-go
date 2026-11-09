// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package codeownership parses GitHub and GitLab CODEOWNERS and resolves
// immutable ownership for repository-relative files.
// It has no dependency on CI initialization or process-global configuration.
//
// The rules and file discovery follow dd-trace-dotnet's implementation:
// https://github.com/DataDog/dd-trace-dotnet/tree/843640c32bae5fe6dcdf790906f6431fe15f6973/tracer/src/Datadog.Trace/Ci/CodeOwnership
// The rule test cases were ported from its test suite at the same commit.
// Upstream is not tracked automatically; those tests pin behavioral parity.
// Deliberate divergences:
//   - GitLab section headers and role owners are scanned instead of matched
//     with upstream's regular expressions.
//   - A rooted GitHub filename prefix such as "/src/ci_*" also owns files
//     below a matching directory, as gitignore does; upstream adds
//     descendants only to directory patterns.
//   - "?" and character classes match one Unicode code point, not one UTF-16
//     code unit, so "?.go" matches "😀.go".
//   - A segment of "*" and an ASCII literal, such as "*go.mod", is compared
//     by suffix without upstream's match step limit; the limit only stops
//     segments far longer than the 255-character file names of common file
//     systems.
//   - Discovery reads only the given directory instead of searching for a Git
//     root, and reads only regular files that resolve inside it.
//   - Tag leaves out owners beyond MaximumTagLength.
//
// As upstream, a GitLab line starting with "[" is a section header, so a
// pattern such as "[0-9]*.md @team" starts a malformed section "0-9" instead
// of adding a rule; the rules after it belong to that section.
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
	"unicode/utf8"
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

// MaximumTagLength is the most characters CI Visibility keeps in a tag value
// (ciVisibilityMetaValueMaxChars in internal/civisibility/integrations).
const MaximumTagLength = 5000

// Email owner limits, in characters, ported from dd-trace-dotnet. GitHub
// accepts only ASCII owners, so there they are also byte counts.
const (
	maximumEmailLocalLength  = 100
	maximumEmailDomainLength = 255
)

// Ownership is immutable. Callers can obtain a defensive owner list, the first
// owner, or the test.codeowners value.
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
// Owners that would make it longer than MaximumTagLength are left out.
func (o *Ownership) Tag() string {
	if o == nil || len(o.owners) == 0 {
		return ""
	}
	if tag := o.tag.Load(); tag != nil {
		return *tag
	}
	tag := encodeTag(o.owners)
	// Concurrent first readers encode the same value; one is kept.
	o.tag.CompareAndSwap(nil, &tag)
	return *o.tag.Load()
}

// encodeTag returns owners as a JSON array of at most MaximumTagLength
// characters, keeping the owners that fit in declaration order, or an empty
// string if none fits. CI Visibility cuts longer tag values, which would leave
// the array unterminated.
func encodeTag(owners []string) string {
	// Almost every list fits and is encoded at once. Escaping only lengthens
	// the array, so a list too long even unescaped, counted with an early
	// stop, goes straight to the owner-by-owner encoding: a huge list is never
	// encoded whole only to be cut.
	unescaped := len("[")
	for _, owner := range owners {
		unescaped += utf8.RuneCountInString(owner) + len(`"",`)
		if unescaped > MaximumTagLength {
			break
		}
	}
	if unescaped <= MaximumTagLength {
		if data, _ := json.Marshal(owners); utf8.RuneCount(data) <= MaximumTagLength {
			return string(data)
		}
	}
	var tag strings.Builder
	length := len("[]")
	for _, owner := range owners {
		data, _ := json.Marshal(owner)
		size := utf8.RuneCount(data)
		if tag.Len() > 0 {
			size++ // separator
		}
		if length+size > MaximumTagLength {
			break
		}
		if tag.Len() == 0 {
			tag.WriteByte('[')
		} else {
			tag.WriteByte(',')
		}
		tag.Write(data)
		length += size
	}
	if tag.Len() == 0 {
		return ""
	}
	tag.WriteByte(']')
	return tag.String()
}

// CodeOwners holds compiled rules. Matching is safe for concurrent readers.
type CodeOwners struct {
	dialect     Dialect
	rules       []rule
	sections    []section
	diagnostics int
	ignored     bool
}
type rule struct {
	glob      globPattern
	ownership *Ownership
	exclusion bool
	key       string // GitLab-only; released after duplicate compaction.
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

// Ignored reports whether the file was ignored, as GitHub ignores a file above
// GitHubMaximumFileSize. An ignored file has no rules.
func (c *CodeOwners) Ignored() bool {
	return c != nil && c.ignored
}

// Load reads one selected file. Invalid lines are diagnosed: GitHub skips
// them, while GitLab keeps the rule with the owners it could extract.
// I/O failures and files that are not regular, such as devices or named
// pipes, return an error. An oversized GitHub file yields empty rules and
// Ignored reports true.
func Load(filename string, dialect Dialect) (*CodeOwners, error) {
	if filename == "" {
		return nil, errors.New("CODEOWNERS path is empty")
	}
	// Check before opening: opening a named pipe blocks until it has a writer,
	// and a device such as /dev/zero reports size 0 and never ends.
	info, err := os.Stat(filename)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("CODEOWNERS is not a regular file: " + filename)
	}
	if dialect == GitHub && info.Size() > GitHubMaximumFileSize {
		return &CodeOwners{dialect: dialect, ignored: true}, nil
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
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
	return c.match(normalizePath(filename))
}

func (c *CodeOwners) match(value string) (*Ownership, bool) {
	if c == nil || value == "" {
		return nil, false
	}
	if c.dialect == GitHub {
		for i := range c.rules {
			if c.rules[i].glob.match(value) {
				return c.rules[i].ownership, true
			}
		}
		return nil, false
	}
	var selected *Ownership
	var combined []string
	var seen map[string]bool
	for _, section := range c.sections {
		owners := section.match(value)
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
func (s section) match(value string) *Ownership {
	var selected *Ownership
	for i := range s.rules {
		r := &s.rules[i]
		if !r.glob.match(value) {
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
	glob, ok := compileGlob(pattern, dialect)
	if !ok {
		return rule{}, false
	}
	return rule{glob: glob, ownership: newOwnership(owners), exclusion: exclusion}, true
}

// Owner tokens are separated by ASCII spaces or tabs.
func ownerSeparator(r rune) bool { return r == ' ' || r == '\t' }
