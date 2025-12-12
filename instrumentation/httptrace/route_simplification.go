// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptrace

import (
	"regexp"
	"strings"
)

var (
	urlPathExtractionRegex = regexp.MustCompile(`^(?P<protocol>[a-z]+://(?P<host>[^?/]+))?(?P<path>/[^?]*)(?P<query>(\?).*)?$`)

	// Route parameter replacement patterns in order of priority
	intPattern   = regexp.MustCompile(`^[1-9][0-9]+$`)
	intIDPattern = regexp.MustCompile(`^(?:[0-9][0-9._-]{2,}|[._-][0-9][0-9._-]+|[._-]{2,}[0-9][0-9._-]*)$`)
	hexPattern   = regexp.MustCompile(`^(?:[0-9][A-Fa-f0-9]{5,}|[A-Fa-f][0-9][A-Fa-f0-9]{4,}|[A-Fa-f]{2}[0-9][A-Fa-f0-9]{3,}|[A-Fa-f]{3}[0-9][A-Fa-f0-9]{2,}|[A-Fa-f]{4}[0-9][A-Fa-f0-9]+|[A-Fa-f]{5,}[0-9][A-Fa-f0-9]*)$`)
	hexIDPattern = regexp.MustCompile(`^(?:[0-9][A-Fa-f0-9._-]{5,}|[A-Fa-f._-][0-9][A-Fa-f0-9._-]{4,}|[A-Fa-f._-]{2}[0-9][A-Fa-f0-9._-]{3,}|[A-Fa-f._-]{3}[0-9][A-Fa-f0-9._-]{2,}|[A-Fa-f._-]{4}[0-9][A-Fa-f0-9._-]+|[A-Fa-f._-]{5,}[0-9][A-Fa-f0-9._-]*)$`)
	strPattern   = regexp.MustCompile(`^(.{20,}|.*[%&'()*+,:=@].*)$`)
)

// maxElems defines the maximum number of non-empty path elements to keep when simplifying a url path.
const maxElems = 8

// simplifyHTTPUrl extracts and simplifies the path from a url.
func simplifyHTTPUrl(url string) string {
	if url == "" {
		return "/"
	}

	path := extractPathFromURL(url)
	if path == "" || path == "/" {
		return "/"
	}

	i, n := 0, len(path)
	count := 0

	var b strings.Builder
	b.Grow(n)
	b.WriteByte('/')

	for count < maxElems {
		// Skip any number of consecutive slashes
		for i < n && path[i] == '/' {
			i++
		}
		if i >= n {
			break
		}

		// Capture the next segment
		start := i
		for i < n && path[i] != '/' {
			i++
		}
		seg := path[start:i]

		simplified := simplifyPathElement(seg)

		if count > 0 {
			b.WriteByte('/') // separator before every element except the first
		}
		b.WriteString(simplified)
		count++
	}

	if count == 0 {
		return "/"
	}
	return b.String()
}

// extractPathFromURL extracts the path component from a URL
func extractPathFromURL(url string) string {
	matches := urlPathExtractionRegex.FindStringSubmatch(url)
	if len(matches) < 4 {
		return ""
	}

	// The path is located in the 3rd capturing group
	return matches[3]
}

// simplifyPathElement applies the parameter replacement rules to a path element
func simplifyPathElement(elem string) string {
	switch {
	case intPattern.MatchString(elem):
		return "{param:int}"
	case intIDPattern.MatchString(elem):
		return "{param:int_id}"
	case hexPattern.MatchString(elem):
		return "{param:hex}"
	case hexIDPattern.MatchString(elem):
		return "{param:hex_id}"
	case strPattern.MatchString(elem):
		return "{param:str}"
	default:
		return elem
	}
}
