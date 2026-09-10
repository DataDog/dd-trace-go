// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"path/filepath"
	"testing"
)

func TestLoadMigrated_RealRepo(t *testing.T) {
	pkgDir := filepath.Join("..", "..", "internal", "config")
	got, err := loadMigrated(pkgDir)
	if err != nil {
		t.Fatalf("loadMigrated: %v", err)
	}
	// These are migrated as of the plan date.
	for _, key := range []string{
		"DD_SERVICE",
		"DD_TRACE_STARTUP_LOGS",
		"DD_TRACE_AGENT_URL",
		"DD_AGENT_HOST",
		"DD_RUNTIME_METRICS_ENABLED",
		"DD_TRACE_RATE_LIMIT",
		"DD_API_KEY",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("expected %s in migrated set", key)
		}
	}
	// DD_APPSEC_ENABLED is registered but not migrated to internal/config.
	if _, ok := got["DD_APPSEC_ENABLED"]; ok {
		t.Errorf("did not expect DD_APPSEC_ENABLED to be in migrated set yet")
	}
}

func TestLoadMigrated_ResolvesPackageConstants(t *testing.T) {
	pkgDir := filepath.Join("..", "..", "internal", "config")
	got, err := loadMigrated(pkgDir)
	if err != nil {
		t.Fatal(err)
	}
	// CIVisibilityEnabledEnvironmentVariable is a constant from internal/civisibility/constants.
	// We require the walker to resolve at least one such cross-package constant.
	if _, ok := got["DD_CIVISIBILITY_ENABLED"]; !ok {
		t.Errorf("expected DD_CIVISIBILITY_ENABLED (resolved from constant) in migrated set")
	}
}
