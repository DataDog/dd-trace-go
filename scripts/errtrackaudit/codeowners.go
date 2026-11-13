// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/codeownership"

// codeownersFile is the repository-relative location of the CODEOWNERS file.
const codeownersFile = "CODEOWNERS"

// unownedPlaceholder groups call sites whose file matches no CODEOWNERS entry,
// or matches an ownerless rule. make lint/misc (scripts/check_codeowners.go)
// enforces that every tracked file is owned, so in practice this should only
// appear on untracked files.
const unownedPlaceholder = "(unowned)"

type codeowners struct {
	rules *codeownership.CodeOwners
}

// loadCodeowners parses the CODEOWNERS file at path with GitHub's rules, as
// this repository is hosted on GitHub.
func loadCodeowners(path string) (*codeowners, error) {
	rules, err := codeownership.Load(path, codeownership.GitHub)
	if err != nil {
		return nil, err
	}
	return &codeowners{rules: rules}, nil
}

// ownersFor returns the owners GitHub would request for a repository-relative
// path. The repository-specific pattern validation remains in
// scripts/check_codeowners.go.
func (c *codeowners) ownersFor(repoRelPath string) []string {
	ownership, ok := c.rules.Match(repoRelPath)
	if !ok || len(ownership.Owners()) == 0 {
		return []string{unownedPlaceholder}
	}
	return ownership.Owners()
}
