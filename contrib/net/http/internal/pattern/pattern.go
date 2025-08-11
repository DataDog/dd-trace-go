// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package pattern

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"

	internal "github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"
	"github.com/puzpuzpuz/xsync/v3"
)

// Route returns the route part of a go1.22 style ServeMux pattern. I.e.
// it returns "/foo" for the pattern "/foo" as well as the pattern "GET /foo".
func Route(s string) string {
	// Support go1.22 serve mux patterns: [METHOD ][HOST]/[PATH]
	// Consider any text before a space or tab to be the method of the pattern.
	// See net/http.parsePattern and the link below for more information.
	// https://pkg.go.dev/net/http#hdr-Patterns-ServeMux
	if i := strings.IndexAny(s, " \t"); i > 0 && len(s) >= i+1 {
		return strings.TrimLeft(s[i+1:], " \t")
	}
	return s
}

// PathParameters return the path parameter values and names from the request.
func PathParameters(pattern string, request *http.Request) map[string]string {
	if pattern == "" {
		return nil
	}
	names := patternNames(pattern)
	res := make(map[string]string, len(names))
	for _, name := range names {
		res[name] = request.PathValue(name)
	}
	return res
}

var patternSegmentsCache = xsync.NewMapOf[string, []string]()

func patternNames(pattern string) []string {
	v, _ := patternSegmentsCache.LoadOrCompute(pattern, func() []string {
		segments, err := parsePatternNames(pattern)
		if err != nil {
			// Ignore the error: Something as gone wrong, but we are not eager to find out why.
			// We will just log it as a telemetry logs warning (and Debug to the user-facing log).
			internal.Instrumentation.Logger().Warn("instrumentation/net/http/pattern: failed to parse mux path pattern %q: %s", pattern, err.Error())
			// here we fallthrough instead of returning to load a nil value into the cache to avoid reparsing the pattern.
		}
		return segments
	})
	return v
}

// parsePatternNames returns the names of the wildcards in the pattern.
// Based on https://cs.opensource.google/go/go/+/refs/tags/go1.23.4:src/net/http/pattern.go;l=84
// but very simplified as we know that the pattern returned must be valid or `net/http` would have panicked earlier.
//
// The pattern string's syntax is
//
//	[METHOD] [HOST]/[PATH]
//
// where:
//   - METHOD is an HTTP method
//   - HOST is a hostname
//   - PATH consists of slash-separated segments, where each segment is either
//     a literal or a wildcard of the form "{name}", "{name...}", or "{$}".
//
// METHOD, HOST and PATH are all optional; that is, the string can be "/".
// If METHOD is present, it must be followed by at least one space or tab.
// Wildcard names must be valid Go identifiers.
// The "{$}" and "{name...}" wildcard must occur at the end of PATH.
// PATH may end with a '/'.
// Wildcard names in a path must be distinct.
//
// Some examples could be:
//   - "/foo/{bar}" returns ["bar"]
//   - "/foo/{bar}/{baz}" returns ["bar", "baz"]
//   - "/foo" returns []
func parsePatternNames(pattern string) ([]string, error) {
	if len(pattern) == 0 {
		return nil, errors.New("empty pattern")
	}
	method, rest, found := pattern, "", false
	if i := strings.IndexAny(pattern, " \t"); i >= 0 {
		method, rest, found = pattern[:i], strings.TrimLeft(pattern[i+1:], " \t"), true
	}
	if !found {
		rest = method
		method = ""
	}

	i := strings.IndexByte(rest, '/')
	if i < 0 {
		return nil, errors.New("host/path missing /")
	}
	host := rest[:i]
	rest = rest[i:]
	if j := strings.IndexByte(host, '{'); j >= 0 {
		return nil, errors.New("host contains '{' (missing initial '/'?)")
	}

	// At this point, rest is the path.
	var names []string
	seenNames := make(map[string]bool)
	for len(rest) > 0 {
		// Invariant: rest[0] == '/'.
		rest = rest[1:]
		if len(rest) == 0 {
			// Trailing slash.
			break
		}
		i := strings.IndexByte(rest, '/')
		if i < 0 {
			i = len(rest)
		}
		var seg string
		seg, rest = rest[:i], rest[i:]
		if i := strings.IndexByte(seg, '{'); i >= 0 {
			// Wildcard.
			if i != 0 {
				return nil, errors.New("bad wildcard segment (must start with '{')")
			}
			if seg[len(seg)-1] != '}' {
				return nil, errors.New("bad wildcard segment (must end with '}')")
			}
			name := seg[1 : len(seg)-1]
			if name == "$" {
				if len(rest) != 0 {
					return nil, errors.New("{$} not at end")
				}
				break
			}
			name, multi := strings.CutSuffix(name, "...")
			if multi && len(rest) != 0 {
				return nil, errors.New("{...} wildcard not at end")
			}
			if name == "" {
				return nil, errors.New("empty wildcard name")
			}
			if !isValidWildcardName(name) {
				return nil, fmt.Errorf("bad wildcard name %q", name)
			}
			if seenNames[name] {
				return nil, fmt.Errorf("duplicate wildcard name %q", name)
			}
			seenNames[name] = true
			names = append(names, name)
		}
	}

	return names, nil
}

func isValidWildcardName(s string) bool {
	if s == "" {
		return false
	}
	// Valid Go identifier.
	for i, c := range s {
		if !unicode.IsLetter(c) && c != '_' && (i == 0 || !unicode.IsDigit(c)) {
			return false
		}
	}
	return true
}
