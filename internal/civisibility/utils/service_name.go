// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package utils

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const defaultCodeOwnersServiceFormat = "service-$(owner)"

var (
	codeOwnersServiceOnce sync.Once
	codeOwnersService     string

	// testPackageDirectory is the directory where the test binary started.
	testPackageDirectory atomic.Pointer[string]
)

// RegisterTestPackageDirectory records the directory where the test binary
// started, which go test sets to the package directory. gotesting registers
// it while packages initialize, before TestMain can change directories. The
// first registration wins.
func RegisterTestPackageDirectory(directory string) {
	testPackageDirectory.CompareAndSwap(nil, &directory)
}

// ServiceFromCodeOwners returns the opt-in service of the test package's
// CODEOWNERS owner, resolved once per process. An explicit DD_SERVICE keeps
// priority, and the environment is never modified. A missing CODEOWNERS file
// or a package outside the workspace returns "", so the usual repository name
// applies; a CODEOWNERS file that does not own the package uses not-owned.
func ServiceFromCodeOwners() string {
	if env.Get("DD_SERVICE") != "" || !internal.BoolEnv(constants.CIVisibilityServiceFromCodeOwnersEnvironmentVariable, false) {
		return ""
	}
	codeOwnersServiceOnce.Do(func() {
		workspace := GetCITags()[constants.CIWorkspacePath]
		if workspace == "" {
			return
		}
		var directory string
		if registered := testPackageDirectory.Load(); registered != nil {
			directory = *registered
		} else if wd, err := os.Getwd(); err == nil {
			directory = wd
		} else {
			return
		}
		relative, ok := relativePathInsideWorkspace(canonicalServiceDirectory(workspace), canonicalServiceDirectory(directory))
		if !ok {
			return
		}
		codeOwnersService = codeOwnersServiceFor(filepath.ToSlash(relative))
	})
	return codeOwnersService
}

// codeOwnersServiceFor returns the service of the package in directory,
// relative to the workspace, or "" without a CODEOWNERS file.
func codeOwnersServiceFor(directory string) string {
	owners := GetCodeOwners()
	if owners == nil {
		return ""
	}
	owner := "not-owned"
	if entry, found := owners.MatchDirectory(path.Clean("/" + directory)); found && entry.FirstOwner() != "" {
		owner = entry.FirstOwner()
		if strings.HasPrefix(owner, "@") {
			owner = strings.TrimLeft(owner, "@")
			if slash := strings.LastIndexByte(owner, '/'); slash >= 0 {
				owner = owner[slash+1:]
			}
		}
		if owner == "" {
			owner = "not-owned"
		}
	}
	format := env.Get(constants.CIVisibilityServiceFromCodeOwnersFormatEnvironmentVariable)
	if format == "" {
		format = defaultCodeOwnersServiceFormat
	}
	service := strings.ReplaceAll(format, "$(owner)", owner)
	log.Debug("civisibility: service from CODEOWNERS package=%q owner=%q service=%q", directory, owner, service)
	return service
}

func canonicalServiceDirectory(directory string) string {
	if resolved, err := filepath.EvalSymlinks(directory); err == nil {
		return resolved
	}
	return filepath.Clean(directory)
}

// ResetServiceFromCodeOwnersForTesting requires all service readers to have
// stopped. It does not change the CODEOWNERS or CI tag caches.
func ResetServiceFromCodeOwnersForTesting() {
	testPackageDirectory.Store(nil)
	codeOwnersServiceOnce = sync.Once{}
	codeOwnersService = ""
}
