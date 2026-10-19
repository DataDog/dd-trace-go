// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package codeownership

import (
	"strings"
	"unicode/utf8"
)

const maximumPatternLength = 1024
const maximumMatchSteps = 65536

type globPattern struct {
	segments []globSegment
	prefix   string
}
type globSegment struct {
	globstar, requiresSegment bool
	tokens                    []segmentToken
	literal                   string // Nonempty ASCII literal; otherwise use tokens.
}
type segmentToken struct {
	kind    byte
	literal rune
	ranges  []runeRange
	negated bool
}
type runeRange struct{ start, end rune }

// File and directory targets share immutable compiled tokens. Their globstar
// requirements differ, so only the small segment lists are copied.
func compileGlobs(pattern string, dialect Dialect) (globPattern, globPattern, bool) {
	parts, firstSlash, trailing := splitPattern(pattern)
	rooted := len(parts) > 0 && parts[0] == "" || dialect == GitHub && firstSlash >= 0 && firstSlash < len(pattern)-1
	first, last := 0, len(parts)
	if rooted && parts[0] == "" {
		first++
	}
	if trailing && last > first && parts[last-1] == "" {
		last--
	}
	file := globPattern{segments: make([]globSegment, 0, len(parts)+2)}
	if !rooted {
		file.addGlobstar(false)
	}
	for i := first; i < last; i++ {
		terminal := i == last-1 && !trailing
		if parts[i] == "**" && !(dialect == GitLab && terminal) {
			file.addGlobstar(terminal)
			continue
		}
		segment, ok := compilePathSegment(parts[i], dialect)
		if !ok {
			return globPattern{}, globPattern{}, false
		}
		file.segments = append(file.segments, segment)
	}
	directory := globPattern{segments: make([]globSegment, len(file.segments), len(file.segments)+1)}
	copy(directory.segments, file.segments)
	for i := range directory.segments {
		directory.segments[i].requiresSegment = false
	}
	// A rooted literal prefix also owns files below a matching directory.
	// Terminal /* keeps its existing immediate-child rule.
	anchoredFilenamePrefix := strings.HasPrefix(pattern, "/") && strings.HasSuffix(pattern, "*") &&
		!strings.HasSuffix(pattern, "/*") && !strings.ContainsAny(pattern[:len(pattern)-1], "*?[]\\")
	if trailing || dialect == GitHub && (literalLastSegment(pattern) || anchoredFilenamePrefix) {
		file.addGlobstar(trailing)
	}
	if strings.Trim(pattern, "/") == "*" {
		directory.segments = directory.segments[:0]
		directory.addGlobstar(false)
	} else if trailing || last > first && parts[last-1] != "*" {
		directory.addGlobstar(false)
	}
	// A rooted literal prefix cannot backtrack. Compare it once instead of
	// splitting the same path and checking each literal for every rule.
	prefixLength, prefixSegments := 0, 0
	for _, segment := range file.segments {
		if segment.literal == "" {
			break
		}
		prefixLength += len(segment.literal) + 1
		prefixSegments++
	}
	if prefixLength != 0 {
		if pattern[0] == '/' && !strings.ContainsRune(pattern, '\\') {
			file.prefix = pattern[:prefixLength]
		} else {
			var prefix strings.Builder
			prefix.Grow(prefixLength)
			for _, segment := range file.segments[:prefixSegments] {
				prefix.WriteByte('/')
				prefix.WriteString(segment.literal)
			}
			file.prefix = prefix.String()
		}
		directory.prefix = file.prefix
		file.segments = file.segments[prefixSegments:]
		directory.segments = directory.segments[prefixSegments:]
	}
	return file, directory, true
}

// ASCII literals need no token array. Escapes, wildcards and non-ASCII use
// compiled rune tokens; matching never allocates a copy of the path.
func compilePathSegment(pattern string, dialect Dialect) (globSegment, bool) {
	literal := len(pattern) != 0
	for i := 0; i < len(pattern); i++ {
		char := pattern[i]
		if char >= utf8.RuneSelf || char == '\\' || char == '*' || char == '?' || char == '[' && dialect == GitLab {
			literal = false
			break
		}
	}
	if literal {
		return globSegment{literal: pattern}, len(pattern) <= maximumPatternLength
	}
	tokens, ok := compileSegment(pattern, dialect)
	return globSegment{tokens: tokens}, ok
}

func (s globSegment) matches(value string) bool {
	if s.literal != "" {
		return s.literal == value
	}
	return matchSegment(s.tokens, value)
}
func (g *globPattern) addGlobstar(required bool) {
	last := len(g.segments) - 1
	if last >= 0 && g.segments[last].globstar {
		g.segments[last].requiresSegment = g.segments[last].requiresSegment || required
		return
	}
	g.segments = append(g.segments, globSegment{globstar: true, requiresSegment: required})
}
func splitPattern(pattern string) ([]string, int, bool) {
	if !strings.ContainsRune(pattern, '\\') {
		return strings.Split(pattern, "/"), strings.IndexByte(pattern, '/'), strings.HasSuffix(pattern, "/")
	}
	var parts []string
	var part strings.Builder
	firstSlash := -1
	trailing := false
	for i := 0; i < len(pattern); i++ {
		char := pattern[i]
		if char == '\\' && i+1 < len(pattern) {
			i++
			if pattern[i] != '/' {
				part.WriteByte(char)
				part.WriteByte(pattern[i])
				trailing = false
				continue
			}
		} else if char != '/' {
			part.WriteByte(char)
			trailing = false
			continue
		}
		if firstSlash < 0 {
			firstSlash = i
		}
		parts = append(parts, part.String())
		part.Reset()
		trailing = i == len(pattern)-1
	}
	parts = append(parts, part.String())
	return parts, firstSlash, trailing
}
func literalLastSegment(pattern string) bool {
	start := strings.LastIndexByte(pattern, '/') + 1
	if start == len(pattern) {
		return false
	}
	for i := start; i < len(pattern); i++ {
		if pattern[i] == '\\' && i+1 < len(pattern) {
			i++
		} else if pattern[i] == '*' || pattern[i] == '?' {
			return false
		}
	}
	return true
}

// Both wildcard levels backtrack only to the latest star. They do not recurse
// or allocate while matching an already normalized GitHub path.
func (g globPattern) match(value string) bool {
	patternIndex, start, star, starStart := 0, -1, -1, -1
	if g.prefix != "" {
		if !strings.HasPrefix(value, g.prefix) || len(value) > len(g.prefix) && value[len(g.prefix)] != '/' {
			return false
		}
		start = nextSegment(value, len(g.prefix))
	} else if len(value) > 1 {
		start = 1
	}
	for start >= 0 {
		if patternIndex < len(g.segments) && g.segments[patternIndex].globstar {
			glob := g.segments[patternIndex]
			star = patternIndex
			patternIndex++
			if glob.requiresSegment {
				start = nextSegment(value, segmentEnd(value, start))
			}
			starStart = start
			continue
		}
		end := segmentEnd(value, start)
		if patternIndex < len(g.segments) && !g.segments[patternIndex].globstar && g.segments[patternIndex].matches(value[start:end]) {
			patternIndex++
			start = nextSegment(value, end)
			continue
		}
		if star < 0 || starStart < 0 {
			return false
		}
		starStart = nextSegment(value, segmentEnd(value, starStart))
		start = starStart
		patternIndex = star + 1
	}
	for patternIndex < len(g.segments) && g.segments[patternIndex].globstar && !g.segments[patternIndex].requiresSegment {
		patternIndex++
	}
	return patternIndex == len(g.segments)
}
func segmentEnd(value string, start int) int {
	if i := strings.IndexByte(value[start:], '/'); i >= 0 {
		return start + i
	}
	return len(value)
}
func nextSegment(value string, end int) int {
	if end < len(value)-1 {
		return end + 1
	}
	return -1
}

func compileSegment(pattern string, dialect Dialect) ([]segmentToken, bool) {
	var storage [64]rune
	chars := storage[:0]
	for _, char := range pattern {
		if len(chars) == maximumPatternLength {
			return nil, false
		}
		chars = append(chars, char)
	}
	if len(chars) > maximumPatternLength {
		return nil, false
	}
	tokens := make([]segmentToken, 0, len(chars))
	for i := 0; i < len(chars); i++ {
		char := chars[i]
		switch char {
		case '\\':
			i++
			if i == len(chars) {
				return nil, false
			}
			tokens = append(tokens, segmentToken{kind: 'l', literal: chars[i]})
		case '*':
			if len(tokens) == 0 || tokens[len(tokens)-1].kind != '*' {
				tokens = append(tokens, segmentToken{kind: '*'})
			}
		case '?':
			tokens = append(tokens, segmentToken{kind: '?'})
		case '[':
			if dialect == GitLab {
				token, end, state := compileClass(chars, i)
				if state < 0 {
					return nil, false
				}
				if state > 0 {
					tokens = append(tokens, token)
					i = end
					continue
				}
			}
			tokens = append(tokens, segmentToken{kind: 'l', literal: char})
		default:
			tokens = append(tokens, segmentToken{kind: 'l', literal: char})
		}
	}
	return tokens, true
}

// state is zero for a literal unclosed [, negative for an invalid class.
func compileClass(pattern []rune, start int) (segmentToken, int, int) {
	token := segmentToken{kind: '['}
	atom := start + 1
	if atom < len(pattern) && (pattern[atom] == '!' || pattern[atom] == '^') {
		token.negated = true
		atom++
	}
	if atom < len(pattern) && pattern[atom] == ']' {
		return token, 0, -1
	}
	close := -1
	for i := atom; i < len(pattern); i++ {
		if pattern[i] == '\\' && i+1 < len(pattern) {
			i++
		} else if pattern[i] == ']' {
			close = i
			break
		}
	}
	if close < 0 {
		return token, 0, 0
	}
	if atom == close {
		return token, 0, -1
	}
	for atom < close {
		a, _, next := classAtom(pattern, atom, close)
		atom = next
		if atom < close {
			sep, escaped, end := classAtom(pattern, atom, close)
			if sep == '-' && !escaped && end < close {
				b, _, end := classAtom(pattern, end, close)
				if a > b {
					return token, 0, -1
				}
				token.ranges = append(token.ranges, runeRange{a, b})
				atom = end
				continue
			}
		}
		token.ranges = append(token.ranges, runeRange{a, a})
	}
	return token, close, 1
}
func classAtom(pattern []rune, index, close int) (rune, bool, int) {
	escaped := pattern[index] == '\\' && index+1 < close
	if escaped {
		index++
	}
	return pattern[index], escaped, index + 1
}

// A cursor keeps a UTF-8 byte offset. Copying it preserves a wildcard's retry
// position without allocating a rune slice for each query.
type runeCursor struct{ offset int }

func (c runeCursor) hasNext(value string) bool { return c.offset < len(value) }
func (c *runeCursor) next(value string) rune {
	if value[c.offset] < utf8.RuneSelf {
		char := rune(value[c.offset])
		c.offset++
		return char
	}
	char, size := utf8.DecodeRuneInString(value[c.offset:])
	c.offset += size
	return char
}
func matchSegment(tokens []segmentToken, value string) bool {
	ti, star := 0, -1
	var cursor, starPath runeCursor
	steps := maximumMatchSteps
	for cursor.hasNext(value) {
		steps--
		if steps < 0 {
			return false
		}
		if ti < len(tokens) && tokens[ti].kind == '*' {
			ti++
			star = ti
			starPath = cursor
			continue
		}
		next := cursor
		char := next.next(value)
		if ti < len(tokens) && tokens[ti].matches(char) {
			ti++
			cursor = next
			continue
		}
		if star < 0 {
			return false
		}
		starPath.next(value)
		cursor = starPath
		ti = star
	}
	for ti < len(tokens) && tokens[ti].kind == '*' {
		ti++
	}
	return ti == len(tokens)
}
func (t segmentToken) matches(char rune) bool {
	switch t.kind {
	case '?':
		return true
	case 'l':
		return t.literal == char
	case '[':
		found := false
		for _, r := range t.ranges {
			if char >= r.start && char <= r.end {
				found = true
				break
			}
		}
		return found != t.negated
	}
	return false
}
