// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import "strings"

type parsedSemver struct {
	major      uint64
	minor      uint64
	patch      uint64
	prerelease string
}

// parseSemver accepts the same version syntax as Rust's semver::Version::parse.
func parseSemver(version string) (parsedSemver, bool) {
	major, next, ok := parseSemverCoreIdentifier(version, 0)
	if !ok || next >= len(version) || version[next] != '.' {
		return parsedSemver{}, false
	}
	minor, next, ok := parseSemverCoreIdentifier(version, next+1)
	if !ok || next >= len(version) || version[next] != '.' {
		return parsedSemver{}, false
	}
	patch, next, ok := parseSemverCoreIdentifier(version, next+1)
	if !ok {
		return parsedSemver{}, false
	}

	parsed := parsedSemver{major: major, minor: minor, patch: patch}
	if next == len(version) {
		return parsed, true
	}

	remainder := version[next:]
	if remainder[0] == '-' {
		remainder = remainder[1:]
		buildStart := strings.IndexByte(remainder, '+')
		if buildStart == -1 {
			if !validSemverIdentifiers(remainder, false) {
				return parsedSemver{}, false
			}
			parsed.prerelease = remainder
			return parsed, true
		}

		parsed.prerelease = remainder[:buildStart]
		if !validSemverIdentifiers(parsed.prerelease, false) {
			return parsedSemver{}, false
		}
		remainder = remainder[buildStart+1:]
	} else if remainder[0] == '+' {
		remainder = remainder[1:]
	} else {
		return parsedSemver{}, false
	}

	if !validSemverIdentifiers(remainder, true) {
		return parsedSemver{}, false
	}
	return parsed, true
}

func parseSemverCoreIdentifier(version string, start int) (uint64, int, bool) {
	if start >= len(version) || !isASCIIDigit(version[start]) {
		return 0, start, false
	}
	if version[start] == '0' {
		return 0, start + 1, true
	}

	const maxUint64 = ^uint64(0)
	var value uint64
	end := start
	for end < len(version) && isASCIIDigit(version[end]) {
		digit := uint64(version[end] - '0')
		if value > (maxUint64-digit)/10 {
			return 0, end, false
		}
		value = value*10 + digit
		end++
	}
	return value, end, true
}

func validSemverIdentifiers(value string, allowLeadingZeros bool) bool {
	identifierStart := 0
	identifierNumeric := true
	for i := 0; i <= len(value); i++ {
		if i == len(value) || value[i] == '.' {
			if i == identifierStart {
				return false
			}
			if !allowLeadingZeros && identifierNumeric && i-identifierStart > 1 && value[identifierStart] == '0' {
				return false
			}
			identifierStart = i + 1
			identifierNumeric = true
			continue
		}

		if !isASCIIAlphanumeric(value[i]) && value[i] != '-' {
			return false
		}
		if !isASCIIDigit(value[i]) {
			identifierNumeric = false
		}
	}
	return true
}

// compareSemver compares semantic version precedence. Build metadata is ignored.
func compareSemver(left, right parsedSemver) int {
	if left.major != right.major {
		if left.major < right.major {
			return -1
		}
		return 1
	}
	if left.minor != right.minor {
		if left.minor < right.minor {
			return -1
		}
		return 1
	}
	if left.patch != right.patch {
		if left.patch < right.patch {
			return -1
		}
		return 1
	}
	return compareSemverPrerelease(left.prerelease, right.prerelease)
}

func compareSemverPrerelease(left, right string) int {
	if left == right {
		return 0
	}
	if left == "" {
		return 1
	}
	if right == "" {
		return -1
	}

	for {
		leftIdentifier, leftRemainder := nextSemverIdentifier(left)
		rightIdentifier, rightRemainder := nextSemverIdentifier(right)
		if ordering := compareSemverIdentifier(leftIdentifier, rightIdentifier); ordering != 0 {
			return ordering
		}

		if leftRemainder == "" {
			if rightRemainder == "" {
				return 0
			}
			return -1
		}
		if rightRemainder == "" {
			return 1
		}
		left = leftRemainder[1:]
		right = rightRemainder[1:]
	}
}

func nextSemverIdentifier(value string) (string, string) {
	if dot := strings.IndexByte(value, '.'); dot != -1 {
		return value[:dot], value[dot:]
	}
	return value, ""
}

func compareSemverIdentifier(left, right string) int {
	leftNumeric := isSemverNumericIdentifier(left)
	rightNumeric := isSemverNumericIdentifier(right)
	if leftNumeric && rightNumeric {
		if len(left) < len(right) {
			return -1
		}
		if len(left) > len(right) {
			return 1
		}
	} else if leftNumeric {
		return -1
	} else if rightNumeric {
		return 1
	}
	return strings.Compare(left, right)
}

func isSemverNumericIdentifier(value string) bool {
	for i := range len(value) {
		if !isASCIIDigit(value[i]) {
			return false
		}
	}
	return true
}

func isASCIIDigit(value byte) bool {
	return value >= '0' && value <= '9'
}

func isASCIIAlphanumeric(value byte) bool {
	return isASCIIDigit(value) || value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}
