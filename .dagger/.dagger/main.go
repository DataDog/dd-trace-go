// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package main is the dd-trace-go Dagger module: one Function per CI
// script/job, each built on the pinned, digest-locked base container under
// ../base/<goVersion>. The same Functions run from a laptop and from a
// GitHub Actions `run:` step, so there is one definition of the test
// environment instead of a workflow-YAML copy that can drift from it.
package main

import (
	"context"
	"fmt"

	"dagger/ci/internal/dagger"
)

type Ci struct{}

var supportedGoVersions = map[string]bool{
	"1.26": true,
	"1.27": true,
}

// Base returns the pinned, tooled container for the given supported Go
// version ("1.26" or "1.27"), built from ../base/<goVersion>/Dockerfile
func (m *Ci) Base(source *dagger.Directory, goVersion string) (*dagger.Container, error) {
	if !supportedGoVersions[goVersion] {
		return nil, fmt.Errorf("unsupported go version %q, want one of 1.26, 1.27", goVersion)
	}
	return source.DockerBuild(dagger.DirectoryDockerBuildOpts{
		Dockerfile: fmt.Sprintf(".dagger/base/go%s/Dockerfile", goVersion),
	}), nil
}

func (m *Ci) GoVersion(ctx context.Context, source *dagger.Directory, goVersion string) (string, error) {
	base, err := m.Base(source, goVersion)
	if err != nil {
		return "", err
	}
	return base.WithExec([]string{"go", "version"}).Stdout(ctx)
}
