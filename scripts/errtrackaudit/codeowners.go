// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import codeownerspkg "github.com/DataDog/dd-trace-go/v2/internal/codeowners"

// codeownersFile is the repository-relative location of the CODEOWNERS file.
const codeownersFile = "CODEOWNERS"

// unownedPlaceholder groups call sites whose file matches no CODEOWNERS entry.
// make lint/misc (scripts/check_codeowners.go) enforces that every tracked
// file is owned, so in practice this should only appear on untracked files.
const unownedPlaceholder = "(unowned)"

type codeowners struct {
	matcher *codeownerspkg.CodeOwners
}

func loadCodeowners(path string) (*codeowners, error) {
	matcher, err := codeownerspkg.New(path)
	if err != nil {
		return nil, err
	}
	return &codeowners{matcher: matcher}, nil
}

// ownersFor returns the owners selected by the shared CODEOWNERS matcher. The
// repository-specific pattern validation remains in scripts/check_codeowners.go.
func (c *codeowners) ownersFor(repoRelPath string) []string {
	entry, ok := c.matcher.Match("/" + repoRelPath)
	if !ok {
		return []string{unownedPlaceholder}
	}
	return entry.Owners
}
