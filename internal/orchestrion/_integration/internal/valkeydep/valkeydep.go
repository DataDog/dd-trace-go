// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package valkeydep creates a valkey-go client inside a dependency module.
package valkeydep

import "github.com/valkey-io/valkey-go"

// NewClient returns a client for addr.
func NewClient(addr string) (valkey.Client, error) {
	return valkey.NewClient(valkey.ClientOption{InitAddress: []string{addr}})
}
