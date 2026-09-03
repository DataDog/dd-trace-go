// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitCmd builds a git invocation that ignores the developer's ambient git
// configuration. Without this, a global hooksPath, a commit template, or gpg
// signing can make workspace setup fail or hang on some machines.
func gitCmd(ctx context.Context, dir string, args ...string) *exec.Cmd {
	base := []string{
		"-c", "core.hooksPath=",
		"-c", "commit.gpgsign=false",
		"-c", "tag.gpgsign=false",
		"-c", "core.quotepath=false",
		"-c", "user.name=dd-trace-go agent-eval",
		"-c", "user.email=agent-eval@localhost",
	}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Dir = dir
	return cmd
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := gitCmd(ctx, dir, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// ResolveRef expands a ref to a full commit SHA. Comparisons must pin SHAs
// rather than branch names, since main moves while a multi-hour run is in flight.
func ResolveRef(ctx context.Context, repoDir, ref string) (string, error) {
	out, err := runGit(ctx, repoDir, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// MaterializeTree writes the repository contents at ref into dest and gives it a
// fresh single-commit history.
//
// The point of going through git archive rather than cloning is contamination:
// a clone (even shallow) leaves the original commit reachable, so an agent asked
// to rebuild deleted code can simply read it back out of history. An archive has
// no history to read.
func MaterializeTree(ctx context.Context, repoDir, ref, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("destination %s is not empty", dest)
	}

	tarball := filepath.Join(filepath.Dir(dest), filepath.Base(dest)+".tar")
	if _, err := runGit(ctx, repoDir, "archive", "--format=tar", "--output="+tarball, ref); err != nil {
		return fmt.Errorf("archive %s: %w", ref, err)
	}
	defer os.Remove(tarball)

	untar := exec.CommandContext(ctx, "tar", "-xf", tarball, "-C", dest)
	if out, err := untar.CombinedOutput(); err != nil {
		return fmt.Errorf("untar %s: %w: %s", tarball, err, strings.TrimSpace(string(out)))
	}

	if _, err := runGit(ctx, dest, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	if _, err := runGit(ctx, dest, "add", "-A"); err != nil {
		return err
	}
	if _, err := runGit(ctx, dest, "commit", "-q", "--no-verify", "-m", "base tree at "+ref); err != nil {
		return err
	}
	return nil
}

// CommitAll records the current tree so a subsequent Diff describes only what
// happened after this point. Used to fold the task mutation into the base state.
func CommitAll(ctx context.Context, dir, message string) error {
	if _, err := runGit(ctx, dir, "add", "-A"); err != nil {
		return err
	}
	// --allow-empty keeps callers from having to care whether anything changed.
	_, err := runGit(ctx, dir, "commit", "-q", "--no-verify", "--allow-empty", "-m", message)
	return err
}

// Diff stages everything and returns the resulting diff plus the changed paths.
// Staging first is what makes files the agent newly created show up; .gitignore
// still applies, so build artifacts are correctly left out.
func Diff(ctx context.Context, dir string) (diff string, changed []string, err error) {
	if _, err := runGit(ctx, dir, "add", "-A"); err != nil {
		return "", nil, err
	}
	names, err := runGit(ctx, dir, "diff", "--cached", "--name-only")
	if err != nil {
		return "", nil, err
	}
	for _, line := range strings.Split(names, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			changed = append(changed, line)
		}
	}
	diff, err = runGit(ctx, dir, "diff", "--cached")
	if err != nil {
		return "", nil, err
	}
	return diff, changed, nil
}

// DiffLineCount counts added and removed lines, excluding file headers.
func DiffLineCount(diff string) int {
	var n int
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
			continue
		case strings.HasPrefix(line, "+"), strings.HasPrefix(line, "-"):
			n++
		}
	}
	return n
}

// PathMatches reports whether path is at or below any of the given prefixes.
// Prefixes are plain path prefixes, not globs: "contrib/twmb/franz-go" matches
// the directory and everything under it, and "go.sum" matches that one file.
func PathMatches(path string, prefixes []string) bool {
	path = filepath.ToSlash(path)
	for _, prefix := range prefixes {
		prefix = strings.TrimSuffix(filepath.ToSlash(prefix), "/")
		if prefix == "" {
			continue
		}
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// AnyPathMatches reports whether any path is at or below any prefix.
func AnyPathMatches(paths, prefixes []string) bool {
	for _, p := range paths {
		if PathMatches(p, prefixes) {
			return true
		}
	}
	return false
}

// AllPrefixesTouched reports whether every prefix has at least one changed path
// under it. This is deliberately stricter than AnyPathMatches: a task that was
// supposed to touch both the contrib and the registration file has not succeeded
// by touching only one.
func AllPrefixesTouched(paths, prefixes []string) bool {
	for _, prefix := range prefixes {
		var found bool
		for _, p := range paths {
			if PathMatches(p, []string{prefix}) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
