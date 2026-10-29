// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package esv7 creates a go-elasticsearch v7 client inside a dependency module.
package esv7

import "github.com/elastic/go-elasticsearch/v7"

// NewClient returns a client for addr.
func NewClient(addr string) (*elasticsearch.Client, error) {
	return elasticsearch.NewClient(elasticsearch.Config{Addresses: []string{addr}})
}
