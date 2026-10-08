// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	codeownerspkg "github.com/DataDog/dd-trace-go/v2/internal/codeowners"

	logger "github.com/DataDog/dd-trace-go/v2/internal/log"
)

// This is a port of https://github.com/DataDog/dd-trace-dotnet/blob/v2.53.0/tracer/src/Datadog.Trace/Ci/CodeOwners.cs

type (
	// CodeOwners represents a structured data type that holds sections of code owners.
	// Each section maps to a slice of entries, where each entry includes a pattern and a list of owners.
	CodeOwners = codeownerspkg.CodeOwners

	// Section represents a block of structured data of multiple entries in a single section.
	Section = codeownerspkg.Section

	// Entry represents a single entry in a CODEOWNERS file.
	Entry = codeownerspkg.Entry
)

var (
	// codeowners holds the parsed CODEOWNERS file data after discovery succeeds.
	codeowners *CodeOwners

	// codeownersLookupDone distinguishes "not looked up yet" from "looked up and no CODEOWNERS file exists".
	codeownersLookupDone bool

	// codeownersMutex protects CODEOWNERS discovery state.
	codeownersMutex sync.Mutex
)

// GetCodeOwners retrieves and caches CODEOWNERS discovery for this process.
// It looks for the CODEOWNERS file in various standard locations within the CI workspace.
// Successful parsed data is cached, and true missing-file discovery results are cached as nil.
// This function is thread-safe due to the use of a mutex.
//
// Returns:
//
//	A pointer to a CodeOwners struct containing the parsed CODEOWNERS data, or nil if not found.
func GetCodeOwners() *CodeOwners {
	codeOwners, _ := GetCodeOwnersWithStatus()
	return codeOwners
}

// GetCodeOwnersWithStatus retrieves CODEOWNERS and reports whether discovery
// reached a cacheable result. A false completion status means a non-missing
// read or parse error occurred and a later call should retry discovery.
func GetCodeOwnersWithStatus() (*CodeOwners, bool) {
	codeownersMutex.Lock()
	defer codeownersMutex.Unlock()

	if codeownersLookupDone {
		return codeowners, true
	}

	hasNonMissingError := false
	tags := GetCITags()
	if v, ok := tags[constants.CIWorkspacePath]; ok {
		paths := []string{
			filepath.Join(v, "CODEOWNERS"),
			filepath.Join(v, ".github", "CODEOWNERS"),
			filepath.Join(v, ".gitlab", "CODEOWNERS"),
			filepath.Join(v, ".docs", "CODEOWNERS"),
		}
		for _, path := range paths {
			if cow, err := parseCodeOwners(path); err == nil {
				codeowners = cow
				codeownersLookupDone = true
				return codeowners, true
			} else if !os.IsNotExist(err) {
				hasNonMissingError = true
			}
		}
	}

	// If the codeowners file is not found, let's try a last resort by looking in the current directory (for standalone test binaries)
	for _, path := range []string{"CODEOWNERS", filepath.Join(filepath.Dir(os.Args[0]), "CODEOWNERS")} {
		if cow, err := parseCodeOwners(path); err == nil {
			codeowners = cow
			codeownersLookupDone = true
			return codeowners, true
		} else if !os.IsNotExist(err) {
			hasNonMissingError = true
		}
	}

	if !hasNonMissingError {
		codeownersLookupDone = true
	}
	return nil, codeownersLookupDone
}

// ResetCodeOwnersForTesting clears the process-local CODEOWNERS discovery cache.
func ResetCodeOwnersForTesting() {
	codeownersMutex.Lock()
	defer codeownersMutex.Unlock()

	codeowners = nil
	codeownersLookupDone = false
}

// parseCodeOwners reads and parses the CODEOWNERS file located at the given filePath.
func parseCodeOwners(filePath string) (*CodeOwners, error) {
	if _, err := os.Stat(filePath); err != nil {
		return nil, err
	}
	cow, err := NewCodeOwners(filePath)
	if err == nil {
		if logger.DebugEnabled() {
			logger.Debug("civisibility: codeowner file '%s' was loaded successfully.", filePath)
		}
		return cow, nil
	}
	logger.Debug("Error parsing codeowners: %s", err.Error())
	return nil, err
}

// NewCodeOwners creates a new instance of CodeOwners by parsing a CODEOWNERS file located at the given filePath.
// It returns an error if the file cannot be read or parsed properly.
func NewCodeOwners(filePath string) (*CodeOwners, error) {
	if filePath == "" {
		return nil, errors.New("filePath cannot be empty")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = file.Close()
		if err != nil && !errors.Is(os.ErrClosed, err) {
			logger.Warn("Error closing codeowners file: %s", err.Error())
		}
	}()
	return codeownerspkg.Parse(file)
}
