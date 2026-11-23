// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package httpsec

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// normalizedRouteTag is the span tag that holds the route of the request as an
// OpenAPI path template (RFC-1125, which amends RFC-1103).
const normalizedRouteTag = "_dd.appsec.normalized_route"

// routePart is a piece of a route: static text, or a path parameter.
type routePart struct {
	// static is the text of a static part, as the framework matches it.
	static string
	// param reports that the part is a path parameter.
	param bool
	// name is the framework name of the parameter. It is empty for an unnamed
	// catch-all.
	name string
	// absent reports that an optional parameter is not in the request, or
	// that its presence cannot be determined. It is left out of the tag.
	absent bool
	// optionalSlash reports that the "/" before the parameter is matched only
	// with the parameter (fiber).
	optionalSlash bool
}

// normalizeRoute returns the normalized route of a request, following the
// route grammar of framework. It returns false when the framework is unknown or
// when the route cannot be expressed as path segments and parameters.
//
// The normalized route describes the current request: an optional parameter or
// a catch-all is in it only when the framework bound a non-empty value to it.
// When params has no value for it, its presence cannot be determined and it is
// left out.
func normalizeRoute(framework, route string, params map[string]string, requestURI string) (string, bool) {
	var (
		parts []routePart
		ok    bool
		// escapes reports that the framework matches a %XX in a route as an
		// escaped byte. Otherwise % is static text.
		escapes bool
	)
	switch framework {
	case "net/http":
		parts, ok = parseServeMuxRoute(route, params, requestURI)
		escapes = true
	case "github.com/gorilla/mux":
		parts, ok = parseBraceRoute(route, params, false)
		if ok {
			parts = appendGorillaRemainder(parts, route, requestURI)
		}
	case "github.com/go-chi/chi", "github.com/go-chi/chi/v5":
		parts, ok = parseBraceRoute(route, params, true)
	case "github.com/gin-gonic/gin":
		parts, ok = parseColonRoute(route, params, true)
	case "github.com/labstack/echo/v4", "github.com/labstack/echo/v5":
		parts, ok = parseColonRoute(route, params, false)
	case "github.com/gofiber/fiber/v2":
		parts, ok = parseFiberRoute(route, params)
		// fiber matches the route with the path as the client sent it, still
		// escaped (unless UnescapePath is set, but then a route with %XX does
		// not match anything).
		escapes = true
	default:
		// Other integrations report a route that is not the router's own
		// (httprouter and httptreemux rebuild it from the request path), or
		// a grammar that is not supported yet.
		return "", false
	}
	if !ok {
		return "", false
	}
	return assembleRoute(parts, escapes, strings.HasSuffix(requestPath(requestURI), "/"))
}

// hasValue reports that the framework bound a value to the catch-all or
// optional parameter key. An empty value, or only "/", is absent. gin starts
// its catch-all values with the "/" before them: trimSlash removes it.
func hasValue(params map[string]string, key string, trimSlash bool) bool {
	v, ok := params[key]
	if trimSlash {
		v = strings.TrimPrefix(v, "/")
	}
	return ok && v != "" && v != "/"
}

// parseServeMuxRoute parses a [net/http.ServeMux] pattern, without its method:
// [HOST]/[PATH], where a segment of PATH is a literal, {name}, {name...} or
// {$}. The host contributes nothing. A pattern that ends with "/" matches a
// whole subtree: the rest of the path is an unnamed catch-all.
func parseServeMuxRoute(route string, params map[string]string, requestURI string) ([]routePart, bool) {
	i := strings.IndexByte(route, '/')
	if i < 0 {
		return nil, false
	}
	// Drop the host (and a method, if the integration left it).
	route = route[i:]

	var parts []routePart
	rest := route
	for len(rest) > 0 {
		// rest starts with "/".
		rest = rest[1:]
		var seg string
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			seg, rest = rest[:i], rest[i:]
		} else {
			seg, rest = rest, ""
		}
		if seg == "" && rest == "" {
			// Trailing slash: a prefix route.
			parts = append(parts,
				routePart{static: "/"},
				routePart{param: true, absent: !hasServeMuxRemainder(route, requestURI)},
			)
			break
		}
		if !strings.HasPrefix(seg, "{") {
			if strings.ContainsAny(seg, "{}") {
				return nil, false
			}
			parts = append(parts, routePart{static: "/" + seg})
			continue
		}
		if !strings.HasSuffix(seg, "}") {
			return nil, false
		}
		name := seg[1 : len(seg)-1]
		if name == "$" {
			// End-of-path anchor: "/users/{$}" is "/users/".
			if rest != "" {
				return nil, false
			}
			parts = append(parts, routePart{static: "/"})
			break
		}
		parts = append(parts, routePart{static: "/"})
		if name, multi := strings.CutSuffix(name, "..."); multi {
			if rest != "" || name == "" {
				return nil, false
			}
			parts = append(parts, routePart{param: true, name: name, absent: !hasValue(params, name, false)})
			break
		}
		if name == "" {
			return nil, false
		}
		parts = append(parts, routePart{param: true, name: name})
	}
	return parts, true
}

// hasServeMuxRemainder reports that the request path continues after the
// segments of the prefix route pattern (which ends with "/"). It returns false
// when the request path is unknown.
func hasServeMuxRemainder(pattern string, requestURI string) bool {
	path := requestPath(requestURI)
	for range strings.Count(pattern, "/") {
		i := strings.IndexByte(path, '/')
		if i < 0 {
			return false
		}
		path = path[i+1:]
	}
	return path != ""
}

// requestPath returns the escaped path of a request URI.
func requestPath(requestURI string) string {
	if i := strings.IndexByte(requestURI, '?'); i >= 0 {
		requestURI = requestURI[:i]
	}
	if strings.HasPrefix(requestURI, "/") || requestURI == "" {
		return requestURI
	}
	// Absolute form, as sent to a proxy.
	u, err := url.Parse(requestURI)
	if err != nil {
		return ""
	}
	return u.EscapedPath()
}

// parseBraceRoute parses a gorilla/mux or go-chi route: {name} or
// {name:regexp} parameters anywhere in a segment. A regexp contributes
// nothing. With chiWildcard, a "*" at the end is chi's unnamed catch-all.
func parseBraceRoute(route string, params map[string]string, chiWildcard bool) ([]routePart, bool) {
	if !strings.HasPrefix(route, "/") {
		return nil, false
	}
	var parts []routePart
	start := 0
	for i := 0; i < len(route); i++ {
		switch route[i] {
		case '{':
			end := closingBrace(route, i)
			if end < 0 {
				return nil, false
			}
			name, _, _ := strings.Cut(route[i+1:end], ":")
			if name == "" {
				return nil, false
			}
			if start < i {
				parts = append(parts, routePart{static: route[start:i]})
			}
			parts = append(parts, routePart{param: true, name: name})
			i = end
			start = end + 1
		case '}':
			return nil, false
		case '*':
			if !chiWildcard || i != len(route)-1 {
				continue
			}
			if start < i {
				parts = append(parts, routePart{static: route[start:i]})
			}
			parts = append(parts, routePart{param: true, absent: !hasValue(params, "*", false)})
			start = len(route)
		}
	}
	if start < len(route) {
		parts = append(parts, routePart{static: route[start:]})
	}
	return parts, true
}

// gorillaPrefixes caches the regexp of gorilla/mux route templates. The routes of
// an application are static, so the cache is bounded by their number.
var gorillaPrefixes sync.Map // string -> *regexp.Regexp, nil when it does not compile

// appendGorillaRemainder adds the implicit unnamed catch-all of a gorilla/mux
// PathPrefix route: the rest of the request path after the part that the route
// template matches. The template does not tell a PathPrefix route from a Path
// route, but a Path route matches the whole path, so it has no rest. The template
// is matched as gorilla matches it: its literal text, a variable with its pattern
// ([^/]+ by default), from the start of the unescaped path.
func appendGorillaRemainder(parts []routePart, route, requestURI string) []routePart {
	if requestURI == "" {
		return parts
	}
	re := gorillaPrefixRegexp(route)
	if re == nil {
		return parts
	}
	path := requestPath(requestURI)
	if unescaped, err := url.PathUnescape(path); err == nil {
		path = unescaped
	}
	loc := re.FindStringIndex(path)
	if loc == nil {
		return parts
	}
	rest := path[loc[1]:]
	if rest == "" || rest == "/" {
		// An empty catch-all is absent.
		return parts
	}
	if strings.HasPrefix(rest, "/") {
		// PathPrefix("/api") hit with "/api/x" is "/api/{param1}".
		parts = append(parts, routePart{static: "/"})
	}
	return append(parts, routePart{param: true})
}

// gorillaPrefixRegexp returns the regexp that matches the gorilla/mux route
// template route at the start of a path, or nil.
func gorillaPrefixRegexp(route string) *regexp.Regexp {
	if re, found := gorillaPrefixes.Load(route); found {
		return re.(*regexp.Regexp)
	}
	var pattern strings.Builder
	pattern.WriteByte('^')
	start := 0
	for i := 0; i < len(route); i++ {
		if route[i] != '{' {
			continue
		}
		end := closingBrace(route, i)
		if end < 0 {
			break
		}
		pattern.WriteString(regexp.QuoteMeta(route[start:i]))
		varPattern := "[^/]+"
		if _, p, found := strings.Cut(route[i+1:end], ":"); found {
			varPattern = p
		}
		pattern.WriteString("(?:" + varPattern + ")")
		i = end
		start = end + 1
	}
	pattern.WriteString(regexp.QuoteMeta(route[start:]))
	re, err := regexp.Compile(pattern.String())
	if err != nil {
		re = nil
	}
	gorillaPrefixes.Store(route, re)
	return re
}

// closingBrace returns the index of the brace that closes the one at start,
// or -1. Braces nest, as in {id:[0-9]{3}}.
func closingBrace(s string, start int) int {
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// parseColonRoute parses a gin or echo route: :name parameters, whose name runs
// to the next "/" (so gin's "/files/:name.json" has a parameter "name.json"),
// and a catch-all at the end. A gin catch-all is named (*name). An echo
// catch-all is unnamed (*), and echo ignores the rest of the route after it.
func parseColonRoute(route string, params map[string]string, gin bool) ([]routePart, bool) {
	if !strings.HasPrefix(route, "/") {
		return nil, false
	}
	var parts []routePart
	start := 0
	for i := 0; i < len(route); i++ {
		switch route[i] {
		case ':':
			end := strings.IndexByte(route[i:], '/')
			if end < 0 {
				end = len(route)
			} else {
				end += i
			}
			name := route[i+1 : end]
			if name == "" {
				return nil, false
			}
			if start < i {
				parts = append(parts, routePart{static: route[start:i]})
			}
			parts = append(parts, routePart{param: true, name: name})
			i = end - 1
			start = end
		case '*':
			if start < i {
				parts = append(parts, routePart{static: route[start:i]})
			}
			if gin {
				name := route[i+1:]
				if name == "" || strings.IndexByte(name, '/') >= 0 {
					return nil, false
				}
				parts = append(parts, routePart{param: true, name: name, absent: !hasValue(params, name, true)})
			} else {
				parts = append(parts, routePart{param: true, absent: !hasValue(params, "*", false)})
			}
			return parts, true
		}
	}
	if start < len(route) {
		parts = append(parts, routePart{static: route[start:]})
	}
	return parts, true
}

// parseFiberRoute parses a fiber v2 route: :name parameters, whose name ends
// at one of "/-.:?\", followed by an optional <constraint> and an optional "?".
// "*" is an optional and "+" a required catch-all, both unnamed (fiber names
// their values *1, +1, ...). "\" escapes the next character. The "/" before an
// optional parameter is optional with it.
func parseFiberRoute(route string, params map[string]string) ([]routePart, bool) {
	if !strings.HasPrefix(route, "/") {
		return nil, false
	}
	var (
		parts             []routePart
		static            strings.Builder
		wildcards, pluses int
		flushStatic       = func() {
			if static.Len() > 0 {
				parts = append(parts, routePart{static: static.String()})
				static.Reset()
			}
		}
		optionalSlashAllowed = func() bool {
			return static.Len() > 0 && strings.HasSuffix(static.String(), "/")
		}
	)
	for i := 0; i < len(route); i++ {
		c := route[i]
		switch c {
		case '\\':
			if i+1 < len(route) {
				i++
				static.WriteByte(route[i])
			}
		case '*', '+':
			var key string
			if c == '*' {
				wildcards++
				key = "*" + strconv.Itoa(wildcards)
			} else {
				pluses++
				key = "+" + strconv.Itoa(pluses)
			}
			slash := c == '*' && optionalSlashAllowed()
			flushStatic()
			parts = append(parts, routePart{param: true, absent: !hasValue(params, key, false), optionalSlash: slash})
		case ':':
			end := i + 1
			for end < len(route) && strings.IndexByte("/-.:?\\<", route[end]) < 0 {
				end++
			}
			name := route[i+1 : end]
			if name == "" {
				return nil, false
			}
			if end < len(route) && route[end] == '<' {
				close := strings.IndexByte(route[end:], '>')
				if close < 0 {
					return nil, false
				}
				end += close + 1
			}
			optional := end < len(route) && route[end] == '?'
			if optional {
				end++
			}
			slash := optional && optionalSlashAllowed()
			flushStatic()
			parts = append(parts, routePart{param: true, name: name, absent: optional && !hasValue(params, name, false), optionalSlash: slash})
			i = end - 1
		default:
			static.WriteByte(c)
		}
	}
	flushStatic()
	return parts, true
}

// assembleRoute writes the normalized route of parts. It returns false when
// the result does not follow the RFC-1125 grammar.
func assembleRoute(parts []routePart, escapes bool, trailingSlash bool) (string, bool) {
	names := paramNames(parts)
	buf := make([]byte, 0, 64)
	skipSlash := false
	for i, part := range parts {
		if part.param {
			if !part.absent {
				buf = append(buf, '{')
				buf = append(buf, names[i]...)
				buf = append(buf, '}')
				skipSlash = false
				continue
			}
			last := i == len(parts)-1
			switch {
			case part.optionalSlash && !(last && trailingSlash):
				// fiber matches "/users/:id?" with "/users".
				if n := len(buf); n > 0 && buf[n-1] == '/' {
					buf = buf[:n-1]
				}
			case len(buf) > 0 && buf[len(buf)-1] == '/':
				// "/a/{x}/b" without x is "/a/b".
				skipSlash = true
			}
			continue
		}
		s := part.static
		if skipSlash {
			s = strings.TrimPrefix(s, "/")
			skipSlash = false
		}
		buf = appendStatic(buf, s, escapes)
	}
	if len(buf) == 0 {
		buf = append(buf, '/')
	}
	route := string(buf)
	if route[0] != '/' || strings.Contains(route, "//") {
		return "", false
	}
	return route, true
}

// paramNames returns the names of the parameters of parts, by index. Names
// keep only A-Z a-z 0-9 _: other characters (each code point) become "_". A
// replaced name that is already used in the route gets the smallest free
// numeric suffix (_2, _3, ...). Unnamed catch-alls get param<N>.
func paramNames(parts []routePart) []string {
	used := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		if part.param && part.name != "" && isValidParamName(part.name) {
			used[part.name] = struct{}{}
		}
	}
	names := make([]string, len(parts))
	unnamed := 0
	for i, part := range parts {
		if !part.param {
			continue
		}
		switch {
		case part.name == "":
			for {
				unnamed++
				name := "param" + strconv.Itoa(unnamed)
				if _, found := used[name]; !found {
					names[i] = name
					break
				}
			}
		case isValidParamName(part.name):
			names[i] = part.name
			continue
		default:
			name := sanitizeParamName(part.name)
			for n := 2; ; n++ {
				if _, found := used[name]; !found {
					break
				}
				name = sanitizeParamName(part.name) + "_" + strconv.Itoa(n)
			}
			names[i] = name
		}
		used[names[i]] = struct{}{}
	}
	return names
}

func isParamNameChar(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_'
}

func isValidParamName(name string) bool {
	for _, r := range name {
		if !isParamNameChar(r) {
			return false
		}
	}
	return true
}

func sanitizeParamName(name string) string {
	return strings.Map(func(r rune) rune {
		if isParamNameChar(r) {
			return r
		}
		return '_'
	}, name)
}

// appendStatic appends the static text s, with every byte other than
// A-Z a-z 0-9 - . _ ~ and the "/" separators percent-encoded with uppercase hex.
// With escapes, a %XX sequence is kept, with its hex uppercased.
func appendStatic(buf []byte, s string, escapes bool) []byte {
	const hex = "0123456789ABCDEF"
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '/' || isUnreserved(c):
			buf = append(buf, c)
		case c == '%' && escapes && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]):
			buf = append(buf, '%', upperHex(s[i+1]), upperHex(s[i+2]))
			i += 2
		default:
			buf = append(buf, '%', hex[c>>4], hex[c&0xF])
		}
	}
	return buf
}

func isUnreserved(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~'
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func upperHex(c byte) byte {
	if c >= 'a' && c <= 'f' {
		return c - 'a' + 'A'
	}
	return c
}
