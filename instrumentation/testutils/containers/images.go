// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package containers

import (
	_ "embed"
	"fmt"
	"sync"

	"go.yaml.in/yaml/v3"
)

//go:embed images/docker-compose.yaml
var imagesYAML []byte

type imageCatalogEntry struct {
	Image string `yaml:"image"`
}

type imageCatalog struct {
	Services map[string]imageCatalogEntry `yaml:"services"`
}

var (
	imagesOnce sync.Once
	images     imageCatalog
)

// Image returns the pinned image reference (including its @sha256 digest) for the given catalog
// key, as defined in images/docker-compose.yaml. That file is the single source of truth for every
// Testcontainers image used by this package and the orchestrion integration suite, and is the only
// thing Dependabot's docker-compose ecosystem needs to track to keep digests fresh.
//
// It panics on an unknown key: a lookup miss here is always a programming error, never a runtime
// condition a caller should handle.
func Image(name string) string {
	imagesOnce.Do(func() {
		if err := yaml.Unmarshal(imagesYAML, &images); err != nil {
			panic(fmt.Sprintf("containers: failed to parse images/docker-compose.yaml: %s", err))
		}
	})
	svc, ok := images.Services[name]
	if !ok {
		panic(fmt.Sprintf("containers: no image catalog entry named %q", name))
	}
	return svc.Image
}
