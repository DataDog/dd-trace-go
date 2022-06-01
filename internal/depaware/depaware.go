// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package depaware maintains a dependency on github.com/tailscale/depware so it
// doesn't get removed from go.mod by "go mod tidy". This lets us pin the
// depware version and run depware with "go run github.com/tailscale/depaware"
// from CI or locally
package depaware

import (
	_ "github.com/tailscale/depaware/depaware" // to create dependency
)
