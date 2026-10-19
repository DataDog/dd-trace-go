// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/codeownership"

	logger "github.com/DataDog/dd-trace-go/v2/internal/log"
)

var (
	// codeowners holds the CODEOWNERS resolver after discovery succeeds.
	codeowners *codeownership.Resolver

	// codeownersLookupDone distinguishes "not looked up yet" from "looked up and no CODEOWNERS file exists".
	codeownersLookupDone bool

	// codeownersMutex protects CODEOWNERS discovery state.
	codeownersMutex sync.Mutex
)

// GetCodeOwners retrieves and caches CODEOWNERS discovery for this process.
// Parsing, host-specific file discovery and matching live in codeownership.
//
// Returns:
//
//	A resolver for paths relative to the CI workspace, or nil if no CODEOWNERS file is found.
func GetCodeOwners() *codeownership.Resolver {
	codeOwners, _ := GetCodeOwnersWithStatus()
	return codeOwners
}

// GetCodeOwnersWithStatus retrieves CODEOWNERS and reports whether discovery
// reached a cacheable result. Missing files and malformed rules are stable
// decisions; a false completion status means an I/O error occurred and a later
// call should retry discovery.
func GetCodeOwnersWithStatus() (*codeownership.Resolver, bool) {
	codeownersMutex.Lock()
	defer codeownersMutex.Unlock()

	if codeownersLookupDone {
		return codeowners, true
	}

	tags := GetCITags()
	locations := codeownership.Locations{
		Workspace:  tags[constants.CIWorkspacePath],
		Repository: tags[constants.GitRepositoryURL],
		Provider:   tags[constants.CIProviderName],
	}
	var resolver *codeownership.Resolver
	var err error
	if locations.Workspace != "" {
		resolver, err = codeownership.Discover(locations)
	}
	if err == nil && resolver == nil {
		// Standalone test binaries may have no CI workspace, or one that does
		// not exist where they run: as a last resort, look in the current
		// directory, then in the binary's directory.
		workingDirectory, _ := os.Getwd()
		for _, directory := range []string{workingDirectory, filepath.Dir(os.Args[0])} {
			resolver, err = codeownership.DiscoverIn(directory, locations)
			if err != nil || resolver != nil {
				break
			}
		}
	}
	if err != nil {
		logger.Debug("civisibility: CODEOWNERS discovery failed: %s", err.Error())
		return nil, false
	}
	codeowners = resolver
	codeownersLookupDone = true
	if resolver != nil {
		logger.Debug("civisibility: CODEOWNERS loaded file=%q dialect=%d diagnostics=%d", resolver.File(), resolver.Dialect(), resolver.Diagnostics())
	}
	return codeowners, true
}

// ResetCodeOwnersForTesting clears the process-local CODEOWNERS discovery cache.
func ResetCodeOwnersForTesting() {
	codeownersMutex.Lock()
	defer codeownersMutex.Unlock()

	codeowners = nil
	codeownersLookupDone = false
}
