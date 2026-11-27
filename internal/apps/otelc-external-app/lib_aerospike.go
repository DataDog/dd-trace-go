// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import as "github.com/aerospike/aerospike-client-go/v7"

func init() {
	registerLibrary("aerospike", func() {
		// The client starts with no node, and a batch with no keys fails before
		// it sends anything.
		policy := as.NewClientPolicy()
		policy.FailIfNotConnected = false
		if c, err := as.NewClientWithPolicy(policy, "127.0.0.1", 1); err == nil {
			_, _ = c.BatchGet(nil, nil)
			c.Close()
		}
	})
}
