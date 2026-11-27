// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package client creates franz-go clients inside a dependency module.
package client

import "github.com/twmb/franz-go/pkg/kgo"

// New returns a client for opts.
func New(opts ...kgo.Opt) (*kgo.Client, error) {
	return kgo.NewClient(opts...)
}
