// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/codeownership"

	logger "github.com/DataDog/dd-trace-go/v2/internal/log"
)

// codeownersRetryInterval bounds discovery retries after an I/O error. Every
// test that needs ownership asks again, so a persistent error would otherwise
// repeat the file system work, under codeownersMutex, for each test.
const codeownersRetryInterval = 100 * time.Millisecond

var (
	// codeowners holds the CODEOWNERS resolver after discovery succeeds.
	codeowners *codeownership.Resolver

	// codeownersLookupDone distinguishes "not looked up yet" from "looked up and no CODEOWNERS file exists".
	codeownersLookupDone bool

	// codeownersRetryAt is when discovery may run again after an I/O error.
	codeownersRetryAt time.Time

	// codeownersFailureLogged limits the warning to the first failed discovery.
	codeownersFailureLogged bool

	// codeownersMutex protects CODEOWNERS discovery state.
	codeownersMutex sync.Mutex

	// codeownersNow lets tests control the retry interval.
	codeownersNow = time.Now
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
// call retries discovery, at most once per codeownersRetryInterval.
func GetCodeOwnersWithStatus() (*codeownership.Resolver, bool) {
	codeownersMutex.Lock()
	defer codeownersMutex.Unlock()

	if codeownersLookupDone {
		return codeowners, true
	}
	now := codeownersNow()
	if now.Before(codeownersRetryAt) {
		return nil, false
	}

	resolver, fallback, err := discoverCodeOwners()
	if err != nil {
		codeownersRetryAt = now.Add(codeownersRetryInterval)
		if codeownersFailureLogged {
			logger.Debug("civisibility: CODEOWNERS discovery failed: %s", err.Error())
		} else {
			codeownersFailureLogged = true
			logger.Warn("civisibility: CODEOWNERS discovery failed; it will be retried: %s", err.Error())
		}
		return nil, false
	}
	codeowners = resolver
	codeownersLookupDone = true
	if resolver != nil {
		logLoadedCodeOwners(resolver, fallback)
	}
	return codeowners, true
}

// discoverCodeOwners reads the CI workspace's CODEOWNERS file, or a last-resort
// file, and reports whether the last resort was used.
func discoverCodeOwners() (*codeownership.Resolver, bool, error) {
	tags := GetCITags()
	locations := codeownership.Locations{
		Repository: tags[constants.GitRepositoryURL],
		Provider:   tags[constants.CIProviderName],
	}
	if workspace := tags[constants.CIWorkspacePath]; workspace != "" {
		// Source paths are relative to the workspace, the repository root, so
		// its CODEOWNERS file is read without looking for an enclosing Git root.
		// An existing workspace decides: a file elsewhere would be matched
		// against paths it was not written for.
		resolver, err := codeownership.DiscoverIn(workspace, locations)
		if err != nil || resolver != nil {
			return resolver, false, err
		}
		if info, err := os.Stat(workspace); err == nil && info.IsDir() {
			return nil, false, nil
		}
	}
	// Standalone test binaries may have no CI workspace, or one that does not
	// exist where they run: as a last resort, look in the current directory,
	// then in the binary's directory. Without a current directory, discovery
	// is retried later rather than settled on a lower-priority file.
	workingDirectory, err := os.Getwd()
	if err != nil {
		return nil, false, err
	}
	for _, directory := range []string{workingDirectory, filepath.Dir(os.Args[0])} {
		resolver, err := codeownership.DiscoverIn(directory, locations)
		if err != nil || resolver != nil {
			return resolver, true, err
		}
	}
	return nil, false, nil
}

// logLoadedCodeOwners warns about rules the parser dropped or an ignored file,
// as dd-trace-dotnet does, so missing ownership can be traced to its cause.
func logLoadedCodeOwners(resolver *codeownership.Resolver, fallback bool) {
	if resolver.Ignored() {
		logger.Warn("civisibility: CODEOWNERS file exceeds GitHub's %d byte limit and is ignored, as GitHub does: %q",
			codeownership.GitHubMaximumFileSize, resolver.File())
	} else if diagnostics := resolver.Diagnostics(); diagnostics > 0 {
		logger.Warn("civisibility: CODEOWNERS file has %d invalid lines; their rules were ignored or kept with the owners that could be read: %q",
			diagnostics, resolver.File())
	}
	if fallback {
		logger.Info("civisibility: CODEOWNERS loaded from a last-resort location outside the CI workspace: %q", resolver.File())
	}
	logger.Debug("civisibility: CODEOWNERS loaded file=%q dialect=%d diagnostics=%d", resolver.File(), resolver.Dialect(), resolver.Diagnostics())
}

// ResetCodeOwnersForTesting clears the process-local CODEOWNERS discovery cache.
func ResetCodeOwnersForTesting() {
	codeownersMutex.Lock()
	defer codeownersMutex.Unlock()

	codeowners = nil
	codeownersLookupDone = false
	codeownersRetryAt = time.Time{}
	codeownersFailureLogged = false
}
