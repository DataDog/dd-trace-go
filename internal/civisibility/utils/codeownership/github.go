// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package codeownership

import (
	"strings"
)

func (c *CodeOwners) parseGitHub(raw string) {
	if raw[0] == '#' {
		return
	}
	if strings.HasPrefix(raw, "\\#") {
		c.diagnostics++
		return
	}
	if comment := findUnescaped(raw, '#'); comment >= 0 {
		raw = strings.TrimSpace(raw[:comment])
	}
	pattern, text := splitRule(raw)
	if pattern == "" || unsupportedGitHubPattern(pattern) {
		c.diagnostics++
		return
	}
	var owners []string
	for owner := range strings.FieldsFuncSeq(text, ownerSeparator) {
		if !validGitHubOwner(owner) {
			c.diagnostics++
			return
		}
		owners = append(owners, owner)
	}
	r, ok := compileRule(pattern, GitHub, uniqueOwners(owners), false)
	if !ok {
		c.diagnostics++
		return
	}
	c.rules = append(c.rules, r)
}
func findUnescaped(value string, char byte) int {
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && i+1 < len(value) {
			i++
		} else if value[i] == char {
			return i
		}
	}
	return -1
}
func unsupportedGitHubPattern(pattern string) bool {
	if strings.HasPrefix(pattern, "!") {
		return true
	}
	opened := false
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '\\' && i+1 < len(pattern) {
			i++
			continue
		}
		if pattern[i] == '[' {
			opened = true
		} else if pattern[i] == ']' && opened {
			return true
		}
	}
	return false
}
func validGitHubOwner(owner string) bool {
	if len(owner) > 1 && owner[0] == '@' && owner[1] != '@' {
		count := 0
		for part := range strings.SplitSeq(owner[1:], "/") {
			count++
			if count > 2 {
				return false
			}
			if part == "" || !asciiWord(part[0]) || !asciiWord(part[len(part)-1]) {
				return false
			}
			for i := 0; i < len(part); i++ {
				if !asciiWord(part[i]) && part[i] != '-' && part[i] != '_' {
					return false
				}
				if i > 0 && part[i] == '-' && part[i-1] == '-' {
					return false
				}
			}
		}
		return true
	}
	at := strings.IndexByte(owner, '@')
	if at < 1 || at > 100 || len(owner)-at-1 < 1 || len(owner)-at-1 > 255 || strings.ContainsRune(owner[at+1:], '@') {
		return false
	}
	for i := range at {
		if !asciiWord(owner[i]) && !strings.ContainsRune(".!#$%&'*+/=?^_`{|}~-", rune(owner[i])) {
			return false
		}
	}
	for i := at + 1; i < len(owner); i++ {
		if !asciiWord(owner[i]) && owner[i] != '.' && owner[i] != '-' && owner[i] != '_' {
			return false
		}
	}
	last := owner[len(owner)-1]
	return asciiWord(last) || last == '_'
}
func asciiWord(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9'
}
