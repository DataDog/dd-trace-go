// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// Only four pattern forms are accepted. Anything else is a parse error.
//
// The restriction is deliberate, and mirrors scripts/check_codeowners.go: this
// matcher has to agree with GitHub's own `paths:` evaluator, and every extra
// glob feature is another chance for the two to disagree. A disagreement here
// does not fail loudly -- it silently stops running a test.
//
//	prefix/**      subtree, and the bare path itself (so a submodule gitlink matches)
//	exact/path.go  equality
//	dir/glob       one segment under dir, glob may contain '*' but no '/'
//	**/glob        that basename at any depth, including the repository root
type pattern struct {
	raw string

	subtree  string // "prefix/**"  -> "prefix"
	exact    string // "exact/path" -> "exact/path"
	dir      string // "dir/glob"   -> "dir";  "**/glob" -> ""
	glob     string // "dir/glob"   -> "glob"; "**/glob" -> "glob"
	anyDepth bool   // "**/glob"
}

func parsePattern(raw string) (pattern, error) {
	switch {
	case raw == "":
		return pattern{}, errors.New("empty pattern")
	case strings.Contains(raw, "***"):
		return pattern{}, fmt.Errorf("%q: '***' is not a supported form", raw)

	case strings.HasSuffix(raw, "/**"):
		prefix := strings.TrimSuffix(raw, "/**")
		if strings.Contains(prefix, "*") {
			return pattern{}, fmt.Errorf("%q: a '/**' pattern may not contain another wildcard", raw)
		}
		return pattern{raw: raw, subtree: prefix}, nil

	case strings.HasPrefix(raw, "**/"):
		glob := strings.TrimPrefix(raw, "**/")
		if strings.Contains(glob, "/") {
			return pattern{}, fmt.Errorf("%q: the part after '**/' must be a single path segment", raw)
		}
		if _, err := path.Match(glob, "probe"); err != nil {
			return pattern{}, fmt.Errorf("%q: %w", raw, err)
		}
		return pattern{raw: raw, glob: glob, anyDepth: true}, nil

	case !strings.Contains(raw, "*"):
		return pattern{raw: raw, exact: raw}, nil

	default:
		dir, glob := path.Split(raw)
		if strings.Contains(dir, "*") {
			return pattern{}, fmt.Errorf("%q: a wildcard is only allowed in the final segment", raw)
		}
		if _, err := path.Match(glob, "probe"); err != nil {
			return pattern{}, fmt.Errorf("%q: %w", raw, err)
		}
		return pattern{raw: raw, dir: strings.TrimSuffix(dir, "/"), glob: glob}, nil
	}
}

func (p pattern) match(file string) bool {
	switch {
	case p.subtree != "":
		// The bare-path case is what makes a git submodule work: a pointer bump
		// shows up in `git diff --name-only` as "openfeature/ffe-system-test-data"
		// with no trailing slash and no children.
		return file == p.subtree || strings.HasPrefix(file, p.subtree+"/")
	case p.exact != "":
		return file == p.exact
	case p.anyDepth:
		ok, _ := path.Match(p.glob, path.Base(file))
		return ok
	default:
		dir, base := path.Split(file)
		if strings.TrimSuffix(dir, "/") != p.dir {
			return false
		}
		ok, _ := path.Match(p.glob, base)
		return ok
	}
}
