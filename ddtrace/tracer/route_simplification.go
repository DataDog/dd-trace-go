// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"regexp"
	"strings"
)

// The simplified HTTP URL path extraction and simplification logic is based
// on the RFC-1051: APM endpoint resource renaming

var (
	urlPathExtractionRegex = regexp.MustCompile(`^(?P<protocol>[a-z]+://(?P<host>[^?/]+))?(?P<path>/[^?]*)(?P<query>(\?).*)?$`)

	// Route parameter replacement patterns in order of priority
	// TODO: Use the lookaround version from the RFC
	intPattern   = regexp.MustCompile(`^[1-9][0-9]+$`)
	intIDPattern = regexp.MustCompile(`^[0-9._-]{3,}$`)
	hexPattern   = regexp.MustCompile(`^[A-Fa-f0-9]{6,}$`)
	hexIDPattern = regexp.MustCompile(`^[A-Fa-f0-9._-]{6,}$`)
	strPattern   = regexp.MustCompile(`^(.{20,}|.*[%&'()*+,:=@].*)$`)
)

// simplifyHTTPUrl extracts and simplifies the path from an HTTP URL according to the RFC
func simplifyHTTPUrl(url string) string {
	if url == "" {
		return "/"
	}

	path := extractPathFromURL(url)
	if path == "" || path == "/" {
		return "/"
	}

	// Split path and filter non-empty elements
	parts := strings.Split(path, "/")
	elements := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			elements = append(elements, part)
			if len(elements) >= 8 {
				break
			}
		}
	}

	if len(elements) == 0 {
		return "/"
	}

	for i, elem := range elements {
		elements[i] = simplifyPathElement(elem)
	}

	return "/" + strings.Join(elements, "/")
}

// extractPathFromURL extracts the path component from a URL (using regex)
func extractPathFromURL(url string) string {
	matches := urlPathExtractionRegex.FindStringSubmatch(url)
	if len(matches) < 4 {
		return ""
	}

	// The path is located in the 3rd capturing group
	return matches[3]
}

// simplifyPathElement applies the parameter replacement rules to a single path element
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
