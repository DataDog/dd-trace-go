// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build ignore

// This tool validates the repository CODEOWNERS file. It asserts that every
// tracked file resolves to an owner, and that GitHub and CI Visibility resolve
// that owner identically.
//
// GitHub and CI Visibility do not implement the same matching rules: GitHub
// follows gitignore semantics, while CI Visibility uses the simpler matcher in
// internal/civisibility/utils. Rather than reconcile the two after the fact,
// this tool restricts patterns to the subset on which they provably agree:
//
//	/path/to/dir/   anchored, applies to everything beneath it
//	/path/to/file   anchored, exact match
//	*suffix         suffix match at any depth
//
// Patterns outside that subset are rejected, so ownership cannot silently
// diverge between the two consumers.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
)

// codeownersPath is the repository-relative location of the CODEOWNERS file.
const codeownersPath = "CODEOWNERS"

// kind is the category of a CODEOWNERS pattern within the supported subset.
type kind int

const (
	kindDir    kind = iota // "/path/" applies to everything beneath the directory
	kindFile               // "/path/file" applies to exactly one file
	kindSuffix             // "*suffix" applies to any path with that suffix
)

// rule is a single validated CODEOWNERS entry.
type rule struct {
	line    int
	pattern string
	kind    kind
	// match is the pattern reduced to the string compared against a path:
	// the leading "/" is dropped for anchored patterns and the leading "*"
	// is dropped for suffix patterns.
	match  string
	owners []string
}

// matches reports whether the rule applies to the given repository-relative path.
func (r rule) matches(path string) bool {
	switch r.kind {
	case kindDir:
		return strings.HasPrefix(path, r.match)
	case kindFile:
		return path == r.match
	case kindSuffix:
		return strings.HasSuffix(path, r.match)
	}
	return false
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "check_codeowners:", err)
		os.Exit(1)
	}
}

func run() error {
	rules, err := parse(codeownersPath)
	if err != nil {
		return err
	}

	files, err := trackedFiles()
	if err != nil {
		return err
	}

	// A trailing slash is what makes a directory pattern recursive for CI
	// Visibility, so an anchored pattern naming a directory must carry one.
	// GitHub would apply it recursively either way, which is exactly the
	// divergence this check exists to prevent.
	var problems []string
	for _, r := range rules {
		if r.kind != kindFile {
			continue
		}
		if isDirPrefix(files, r.match) {
			problems = append(problems, fmt.Sprintf(
				"%s:%d: %q names a directory but has no trailing slash; write %q so it applies recursively for CI Visibility too",
				codeownersPath, r.line, r.pattern, r.pattern+"/"))
		}
	}

	civis, err := utils.NewCodeOwners(codeownersPath)
	if err != nil {
		return fmt.Errorf("parsing %s with the CI Visibility matcher: %w", codeownersPath, err)
	}

	var unowned, diverged []string
	for _, f := range files {
		owners, pattern, ok := resolve(rules, f)
		if !ok {
			unowned = append(unowned, f)
			continue
		}
		// CI Visibility matches against absolute repository paths.
		entry, matched := civis.Match("/" + f)
		var civisOwners []string
		if matched {
			civisOwners = entry.Owners
		}
		if normalize(owners) != normalize(civisOwners) {
			diverged = append(diverged, fmt.Sprintf(
				"  %s\n      GitHub         (%s): %s\n      CI Visibility  (%s): %s",
				f, pattern, normalize(owners), entry.Pattern, normalize(civisOwners)))
		}
	}

	if len(unowned) > 0 {
		problems = append(problems, fmt.Sprintf(
			"%d file(s) have no owner in %s:\n%s\nAdd an entry so the right team is asked to review these paths.",
			len(unowned), codeownersPath, bullets(unowned)))
	}
	if len(diverged) > 0 {
		problems = append(problems, fmt.Sprintf(
			"%d file(s) resolve to different owners for GitHub and CI Visibility:\n%s",
			len(diverged), strings.Join(diverged, "\n")))
	}

	if len(problems) > 0 {
		return errors.New("\n" + strings.Join(problems, "\n\n"))
	}

	fmt.Printf("check_codeowners: %d tracked files, all owned; GitHub and CI Visibility agree\n", len(files))
	return nil
}

// parse reads the CODEOWNERS file and validates every entry against the
// supported pattern subset.
func parse(path string) ([]rule, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var (
		rules    []rule
		problems []string
	)
	scanner := bufio.NewScanner(f)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}

		fields := strings.Fields(text)
		pattern, owners := fields[0], fields[1:]

		k, match, err := classify(pattern)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s:%d: %q: %s", path, line, pattern, err))
			continue
		}
		if len(owners) == 0 {
			problems = append(problems, fmt.Sprintf("%s:%d: %q has no owners", path, line, pattern))
			continue
		}
		for _, o := range owners {
			// The CI Visibility parser treats any term containing "@" as an
			// owner and everything else as part of the pattern, so a stray
			// token would silently corrupt the entry.
			if !strings.Contains(o, "@") {
				problems = append(problems, fmt.Sprintf(
					"%s:%d: %q is not an owner; trailing comments are not supported on entry lines", path, line, o))
			}
		}
		rules = append(rules, rule{line: line, pattern: pattern, kind: k, match: match, owners: owners})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(problems) > 0 {
		return nil, errors.New("\n" + strings.Join(problems, "\n"))
	}
	return rules, nil
}

// classify validates a pattern and reduces it to its comparison form.
func classify(pattern string) (kind, string, error) {
	switch {
	case strings.HasPrefix(pattern, "*"):
		suffix := pattern[1:]
		switch {
		case suffix == "":
			return 0, "", errors.New(`the "*" catch-all is not allowed; every path must have an explicit owner`)
		case strings.Contains(suffix, "*"):
			return 0, "", errors.New(`only a single leading "*" is supported by the CI Visibility matcher`)
		case strings.Contains(suffix, "/"):
			return 0, "", errors.New(`a "*" pattern must be a bare suffix containing no "/", e.g. "*appsec.go"`)
		}
		return kindSuffix, suffix, nil

	case strings.HasPrefix(pattern, "/"):
		if strings.Contains(pattern, "*") {
			return 0, "", errors.New(`wildcards are not supported in anchored paths; use a "/dir/" or "*suffix" pattern instead`)
		}
		if strings.HasSuffix(pattern, "/") {
			return kindDir, pattern[1:], nil
		}
		return kindFile, pattern[1:], nil

	default:
		return 0, "", errors.New(`pattern must be anchored with a leading "/" or be a "*suffix" pattern`)
	}
}

// resolve returns the owners of a path. The last matching entry wins, which is
// the precedence rule shared by GitHub and CI Visibility.
func resolve(rules []rule, path string) (owners []string, pattern string, ok bool) {
	for _, r := range rules {
		if r.matches(path) {
			owners, pattern, ok = r.owners, r.pattern, true
		}
	}
	return owners, pattern, ok
}

// isDirPrefix reports whether any tracked file lives beneath the given path.
func isDirPrefix(files []string, path string) bool {
	prefix := path + "/"
	for _, f := range files {
		if strings.HasPrefix(f, prefix) {
			return true
		}
	}
	return false
}

// trackedFiles lists every file tracked by git, relative to the repository root.
func trackedFiles() ([]string, error) {
	out, err := exec.Command("git", "ls-files", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("listing tracked files: %w", err)
	}
	files := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if len(files) == 1 && files[0] == "" {
		return nil, errors.New("no tracked files found; run this from the repository root")
	}
	return files, nil
}

// normalize renders an owner list in a stable, comparable form.
func normalize(owners []string) string {
	if len(owners) == 0 {
		return "(none)"
	}
	sorted := append([]string(nil), owners...)
	sort.Strings(sorted)
	return strings.Join(sorted, " ")
}

// bullets formats paths as an indented list, truncated to keep output readable.
func bullets(paths []string) string {
	const max = 50
	var b strings.Builder
	for i, p := range paths {
		if i == max {
			fmt.Fprintf(&b, "  ... and %d more\n", len(paths)-max)
			break
		}
		fmt.Fprintf(&b, "  %s\n", p)
	}
	return strings.TrimRight(b.String(), "\n")
}
