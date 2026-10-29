// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package esv8 creates a go-elasticsearch v8 client inside a dependency module.
package esv8

import "github.com/elastic/go-elasticsearch/v8"

// NewClient returns a client for addr.
func NewClient(addr string) (*elasticsearch.Client, error) {
	return elasticsearch.NewClient(elasticsearch.Config{Addresses: []string{addr}})
}
