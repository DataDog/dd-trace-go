// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package vaultconfig builds a Vault config inside a dependency module.
package vaultconfig

import (
	"github.com/hashicorp/vault/api"
)

// New returns a config for the Vault server at addr.
func New(addr string) *api.Config {
	return &api.Config{Address: addr}
}
