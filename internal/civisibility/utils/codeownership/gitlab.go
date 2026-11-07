// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package codeownership

import (
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type gitLabParser struct {
	sections []section
	named    map[string]int
	current  int
	defaults []string
}

func (p *gitLabParser) parseLine(raw string, diagnostics *int) {
	if strings.HasPrefix(raw, "[") || strings.HasPrefix(raw, "^[") {
		name, defaults, parsed, bad := parseSectionHeader(raw)
		if bad || !parsed {
			(*diagnostics)++
		}
		if !parsed {
			return
		}
		p.defaults = defaults
		key := strings.ToUpper(name)
		index, found := p.named[key]
		if !found {
			index = len(p.sections)
			p.named[key] = index
			p.sections = append(p.sections, section{})
		}
		p.current = index
		return
	}
	if raw[0] == '#' {
		return
	}
	pattern, text := splitRule(raw)
	exclusion := strings.HasPrefix(pattern, "!")
	if exclusion {
		pattern = pattern[1:]
	}
	if pattern == "" {
		(*diagnostics)++
		return
	}
	var owners []string
	valid := true
	if !exclusion {
		owners, valid = gitLabOwners(text)
		if text == "" {
			owners = p.defaults
		}
		if len(owners) == 0 {
			valid = false
		}
	}
	r, ok := compileRule(pattern, GitLab, owners, exclusion)
	if !ok {
		(*diagnostics)++
		return
	}
	if !valid {
		(*diagnostics)++
	}
	r.key = gitLabPatternKey(pattern)
	p.sections[p.current].rules = append(p.sections[p.current].rules, r)
}
func (p *gitLabParser) finish() []section {
	for i := range p.sections {
		rules := p.sections[i].rules
		slices.Reverse(rules)
		seen := make(map[string]bool, len(rules))
		out := rules[:0]
		for _, r := range rules {
			if !seen[r.key] {
				seen[r.key] = true
				r.key = ""
				out = append(out, r)
			}
		}
		clear(rules[len(out):])
		p.sections[i].rules = out
		for _, r := range out {
			if r.exclusion {
				p.sections[i].hasExclusions = true
				break
			}
		}
	}
	return p.sections
}

// A recognizable header changes the active section even when its suffix is
// malformed. Only the recognized owner span supplies defaults, as in GitLab.
func parseSectionHeader(raw string) (string, []string, bool, bool) {
	i := 0
	optional := raw[0] == '^'
	if optional {
		i++
	}
	if i >= len(raw) || raw[i] != '[' {
		return "", nil, false, false
	}
	close := strings.IndexByte(raw[i+1:], ']')
	if close < 0 {
		return "", nil, false, false
	}
	close += i + 1
	name := strings.TrimSpace(raw[i+1 : close])
	i = close + 1
	bad := name == ""
	approvalRecognized := false
	if i < len(raw) && raw[i] == '[' {
		end := strings.IndexByte(raw[i+1:], ']')
		if end >= 0 {
			end += i + 1
			number := raw[i+1 : end]
			recognized := true
			for _, r := range number {
				if !unicode.IsSpace(r) && !sectionDigit(r) {
					recognized = false
					break
				}
			}
			if recognized {
				approvalRecognized = true
				n, err := strconv.ParseInt(number, 10, 32)
				if err != nil || optional && n > 0 {
					bad = true
				}
				i = end + 1
			}
		}
	}
	strict := true
	if i < len(raw) {
		r, _ := utf8.DecodeRuneInString(raw[i:])
		strict = unicode.IsSpace(r)
	}
	end := i
	for end < len(raw) {
		r, size := utf8.DecodeRuneInString(raw[end:])
		if !sectionOwnerCharacter(r) {
			break
		}
		end += size
	}
	owners, valid := gitLabOwners(raw[i:end])
	// Approval whitespace is accepted by the permissive parser, but not by the
	// strict header grammar. Empty/malformed approval counts are also diagnosed.
	if approvalRecognized {
		start := strings.Index(raw, "][") + 2
		finish := strings.IndexByte(raw[start:], ']') + start
		if start >= 2 && finish >= start {
			for _, r := range raw[start:finish] {
				if !sectionDigit(r) {
					strict = false
				}
			}
		}
	}
	return name, owners, true, bad || !strict || end < len(raw) || !valid
}
func sectionDigit(r rune) bool { return unicode.IsDigit(r) }
func sectionOwnerCharacter(r rune) bool {
	return ownerWordCharacter(r) || unicode.IsSpace(r) || strings.ContainsRune("@.-/", r)
}

// Owner extraction scans namespaces, roles and emails independently. Length
// limits count Unicode code points, and classification uses Go Unicode tables.
func gitLabOwners(text string) ([]string, bool) {
	var owners []string
	seen := make(map[string]bool)
	valid := true
	add := func(owner string) {
		if !seen[owner] {
			seen[owner] = true
			owners = append(owners, owner)
		}
	}
	for token := range strings.FieldsFuncSeq(text, ownerSeparator) {
		var storage [128]rune
		chars := storage[:0]
		for _, char := range token {
			chars = append(chars, char)
		}
		start, end, ok := findNamespace(chars, 0)
		if ok && start == 0 && end == len(chars) || validGitLabRole(chars) {
			add(token)
			continue
		}
		found := false
		for search := 0; ; {
			start, end, ok = findNamespace(chars, search)
			if !ok {
				break
			}
			add(string(chars[start:end]))
			found = true
			search = end
		}
		for at := 0; at+1 < len(chars); at++ {
			if chars[at] != '@' || chars[at+1] != '@' || at > 0 && (ownerWordCharacter(chars[at-1]) || chars[at-1] == '@') {
				continue
			}
			end := at + 2
			for end < len(chars) && !unicode.IsSpace(chars[end]) {
				end++
			}
			if validGitLabRole(chars[at:end]) {
				add(string(chars[at:end]))
				found = true
			}
		}
		for search := 0; ; {
			start, end, ok = findEmail(chars, search)
			if !ok {
				break
			}
			email := chars[start:end]
			if _, _, hasNamespace := findNamespace(email, 0); !hasNamespace {
				add(string(email))
				found = true
			}
			search = end
		}
		if !found {
			valid = false
		}
	}
	return owners, valid
}
func validGitLabRole(chars []rune) bool {
	if len(chars) < 3 || chars[0] != '@' || chars[1] != '@' {
		return false
	}
	// Role names are ASCII identifiers and accept ASCII case variants.
	for _, role := range []string{"developer", "developers", "maintainer", "maintainers", "owner", "owners"} {
		if len(chars)-2 != len(role) {
			continue
		}
		equal := true
		for i, unit := range chars[2:] {
			if unit >= 'A' && unit <= 'Z' {
				unit += 'a' - 'A'
			}
			if unit != rune(role[i]) {
				equal = false
				break
			}
		}
		if equal {
			return true
		}
	}
	return false
}
func findNamespace(token []rune, start int) (int, int, bool) {
	for at := start; at < len(token); at++ {
		if token[at] != '@' || at+1 == len(token) {
			continue
		}
		if at > 0 && (wordCharacter(token[at-1]) || token[at-1] == '@') {
			continue
		}
		if !namespaceStart(token[at+1]) {
			continue
		}
		segmentStart, lastEnd := at+1, -1
		for i := segmentStart; i < len(token); i++ {
			char := token[i]
			if char == '/' {
				if i == segmentStart || lastEnd != i {
					break
				}
				segmentStart = i + 1
				continue
			}
			if i == segmentStart && !namespaceStart(char) || !namespaceStart(char) && char != '-' {
				break
			}
			if asciiNamespaceCharacter(char) || char == '_' || char == '-' {
				lastEnd = i + 1
			}
		}
		if lastEnd > at+1 {
			return at, lastEnd, true
		}
	}
	return 0, 0, false
}
func asciiNamespaceCharacter(char rune) bool { return char < 128 && asciiWord(byte(char)) }
func namespaceStart(char rune) bool {
	return asciiNamespaceCharacter(char) || char == '_' || char == '.'
}
func wordCharacter(char rune) bool {
	return unicode.IsLetter(char) || unicode.IsDigit(char) || char == '_'
}
func ownerWordCharacter(char rune) bool {
	return wordCharacter(char) || unicode.Is(unicode.Mn, char) || unicode.Is(unicode.Pc, char)
}
func findEmail(token []rune, search int) (int, int, bool) {
	for at := search; at < len(token); at++ {
		if token[at] != '@' {
			continue
		}
		local, length := at, 0
		for local > search && length < 100 {
			r := token[local-1]
			if r == '@' || unicode.IsSpace(r) {
				break
			}
			local--
			length++
		}
		if length == 0 {
			continue
		}
		end, lastWord := at+1, -1
		limit := min(len(token), end+255)
		for end < limit {
			if token[end] == '@' || unicode.IsSpace(rune(token[end])) {
				break
			}
			if ownerWordCharacter(token[end]) {
				lastWord = end + 1
			}
			end++
		}
		if lastWord > at+1 {
			return local, lastWord, true
		}
	}
	return 0, 0, false
}
func gitLabPatternKey(pattern string) string {
	if pattern == "*" {
		return "/**/*"
	}
	key := pattern
	if strings.ContainsRune(pattern, '\\') {
		var b strings.Builder
		b.Grow(len(pattern))
		for i := 0; i < len(pattern); i++ {
			if pattern[i] == '\\' && i+1 < len(pattern) {
				next, _ := utf8.DecodeRuneInString(pattern[i+1:])
				if i == 0 && next == '#' || unicode.IsSpace(next) {
					i++
				}
			}
			b.WriteByte(pattern[i])
		}
		key = b.String()
	}
	if !strings.HasPrefix(key, "/") {
		key = "/**/" + key
	}
	if strings.HasSuffix(key, "/") {
		key += "**/*"
	}
	return key
}
